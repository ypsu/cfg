package gdsnap

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

func usage() {
	o := flag.CommandLine.Output()
	fmt.Fprintln(o, `gdsnap: google drive snapshotter
usage: gdsnap [global flags...] subcommand [args...]

periodically snapshots the changed files of a directory to gdrive as a backup.
to use: set up the right -dir and -gdir flags (ideally in a config file),
then use the auth subcommand to authorize,
and then run the watch command as a daemon to back up data continuosly.
if you want to restore, use the restore subcommand.
you can alter the -dir flag for restore in case you want to restore files somewhere else.

subcommands:
  auth: authorize a gdrive account for gdsnap.
  cat: prints a file from the archive.
  diff: diff the whole tree or specific files. the diff is between gdrive and the files on disk.
  help: print help about a subcommand.
  list: list gdrive metadata.
  quota: print gdrive quota usage and limit.
  restore: restores files from the backup (destructive operation!).
  save: snapshot a specific file.
  watch: watch target directory for changes and back them up.

config files:
  gdsnap reads in ~/.gdsnap, ~/.config/gdsnap and finally ~/.cache/gdsnap config files.
  each config line is either a comment line starting with # or of the following format:
    [profile] [flagname] "[value in quotes]"
  profile is usually the hostname on which the flag applies or * to apply to all profiles.
  it allows using a single config file for configuring backups for multiple devices.
  example:
    * refreshtoken "abcdef"
    * gdir "1234"
    # work stuff goes somewhere else.
    worklaptop dir "/home/myuser/work"
    worklaptop gdir "5678"
  the refreshtoken is sensitive piece of data, you might want to put that into the separate .cache/gdsnap file.

globs:
  the globs can contain "*" or "**", other wildcards like "?" are not supported.
  "**" matches / (the directory separator) too.
  e.g. ".cache/**", say, for the -ignore means to ignore all files under the .cache directory.
  cat/diff/list/restore accept globs as arguments.
  globs starting with / are absolute globs and the root is relative to -dir.
  the current relative path from dir is prepended for relative globs.
  e.g. .gitignore will be translated to $(dir)/path/to/currentwd/.gitignore.
  thanks to this cat/diff/list/restore are easy to use with files in the current directory.

signals:
  during the watch command sigint (ctrl+c) triggers an early backup cycle.
  use sigquit to quit (ctrl+/).
  otherwise sigint is fine for cancelling all the other operations.

implementation details:
  all the files are backed up to gdrive.
  the contents are encrypted but the filenames are not to keep the system easy to debug.
  gdrive tracks the last 100 revisions of each file so those can restored too.
  use the -t flag to specify a revision other than the head revision.
  some file metadata is stored in the mimetype of the revision:
    - gdsnap/deleted: the file is non-existent at this revision.
      if this is the head version, the file is also moved to the trash
      which is then deleted after 30 days.
    - gdsnap/symlink: the file is a symlink and the contents is the target file.
      the contents of this is not encrypted.
    - gdsnap/data???: ordinary file. ??? is an octal number of the permissions
      that restore will use when restoring a file.

cleanup:
  if you want to purge your data from gdrive
  then move all the gdsnap related files (i.e. all files from your backup directory) to trash.
  then empty your trash and the data is then irrevocably gone from gdrive.

global flags:`)
	flag.PrintDefaults()
}

var (
	cycledurFlag     = flag.Duration("cycledur", 20*time.Minute, "the time to wait between backup cycles. relevant only for the watch subcommand.")
	dirFlag          = flag.String("dir", os.Getenv("PWD"), "the root directory under which to operate recursively.")
	gdirFlag         = flag.String("gdir", "", "the gdrive directory under which to to save the files.")
	ignoreFlag       = flag.String("ignore", "", "comma separated list of globs that save/watch ignores to upload.")
	sizelimitmbFlag  = flag.Int("sizelimitmb", 20, "size limit of the maximum file in megabytes. make sure to pick a limit that comfortably fits into memory.")
	passwordFlag     = flag.String("password", "", "the password to encrypt the files with. if empty, the files are encrypted with an empty password.")
	profileFlag      = flag.String("profile", hostname(), "flag defaults selector for the gdsnap config files.")
	refreshtokenFlag = flag.String("refreshtoken", "", "the oauth2 refresh token needed for accessing gdrive. generate one with the auth subcommand.")
	tFlag            = flag.String("t", "", "time offset for cat/diff/restore operations. either a duration from now or an absolute utc time value. default is the head revision for each file.")
	warncmdFlag      = flag.String("warncmd", "", "run command on warning-level events. the command should notify you about the event. static flags can be specified, separate them with space.")
)

const (
	oaClientID = "1076973936178-12a8rasuan6erslop5nkqe088cce31p8.apps.googleusercontent.com"
	oaSecret   = "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"
)

type fileinfo struct {
	Name         string
	ID           string
	Size         string
	Trashed      bool
	MimeType     string
	ModifiedTime string
}

type gdsnap struct {
	accesstoken string
	tokenbirth  time.Time
	files       map[string]fileinfo
	ignore      []string
	aead        cipher.AEAD
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		log.Fatalf("couldn't look up hostname: %v", err)
	}
	return h
}

// matchglob matches simple globs.
// only supports * and **.
// * matches everything except /, ** matches / too.
func matchglob(pattern, name string) bool {
	laststar, lastdstar := -1, -1
	p, n := 0, 0
	for n < len(name) {
		if p == len(pattern) {
			if name[n] == '/' {
				if lastdstar != -1 {
					laststar = lastdstar
					p = lastdstar
					n++
					continue
				}
				return false
			}
			if laststar != -1 {
				p = laststar
				n++
				continue
			}
			return false
		}
		if pattern[p] == '*' {
			stars := 0
			for p < len(pattern) && pattern[p] == '*' {
				p++
				stars++
			}
			laststar = p
			if stars >= 2 {
				lastdstar = p
			}
			continue
		}
		if name[n] == '/' {
			laststar = -1
		}
		if name[n] == pattern[p] {
			n++
			p++
		} else {
			if laststar != -1 {
				p = laststar
			} else if lastdstar != -1 {
				laststar = lastdstar
				p = lastdstar
			} else {
				return false
			}
			n++
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// fullglobs prepends the local directory to the relative globs from the args.
func fullglobs(args []string) []string {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("couldn't determine current working dir: %v", err)
	}
	pathprefix, err := filepath.Rel(*dirFlag, cwd)
	if err != nil || strings.HasPrefix(pathprefix, "..") {
		pathprefix = ""
	}
	globs := make([]string, len(args))
	for i, a := range args {
		if !strings.HasPrefix(a, "/") {
			a = path.Join(pathprefix, a)
		}
		globs[i] = strings.TrimLeft(a, "/")
	}
	return globs
}

// filterfiles returns the list of filenames that match at least one of the globs.
// the current directory will be added to the relative entries in globs.
func filterfiles(files map[string]fileinfo, globs []string) []string {
	globs = fullglobs(globs)
	filelist := []string{}
	for f := range files {
		if len(globs) > 0 {
			match := false
			for _, glob := range globs {
				if matchglob(glob, f) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		filelist = append(filelist, f)
	}
	sort.Strings(filelist)
	return filelist
}

// watchdir streams the changed filenames on filech.
func watchdir(filech chan<- string) {
	log.Printf("initializing inotify rooted at %q.", *dirFlag)
	watches := map[int]string{}
	ifd, err := syscall.InotifyInit()
	if err != nil {
		log.Fatal(err)
	}
	var watchpath func(string)
	watchpath = func(dirpath string) {
		var mask uint32
		mask |= syscall.IN_CLOSE_WRITE
		mask |= syscall.IN_CREATE
		mask |= syscall.IN_DELETE
		mask |= syscall.IN_MOVED_FROM
		mask |= syscall.IN_MOVED_TO
		mask |= syscall.IN_DONT_FOLLOW
		mask |= syscall.IN_EXCL_UNLINK
		mask |= syscall.IN_ONLYDIR
		var wd int
		if wd, err = syscall.InotifyAddWatch(ifd, dirpath, mask); err != nil {
			log.Fatal(err)
		}
		watches[wd] = dirpath

		walkfunc := func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Printf("[warning] can't walk %s: %v", path, err)
				warn()
				return nil
			}
			if !d.IsDir() {
				filech <- path
				return nil
			}
			if path == dirpath {
				return nil
			}
			watchpath(path)
			return fs.SkipDir
		}
		filepath.WalkDir(dirpath, walkfunc)
	}
	watchpath(*dirFlag)

	log.Print("watching inotify events.")
	for {
		const bufsize = 16384
		eventbuf := [bufsize]byte{}
		n, err := syscall.Read(ifd, eventbuf[:])
		if n <= 0 || err != nil {
			log.Fatal(err)
		}
		for offset := 0; offset < n; {
			if n-offset < syscall.SizeofInotifyEvent {
				log.Fatalf("invalid inotify read: n:%d offset:%d.", n, offset)
			}
			event := (*syscall.InotifyEvent)(unsafe.Pointer(&eventbuf[offset]))
			wd := int(event.Wd)
			mask := int(event.Mask)
			namelen := int(event.Len)
			namebytes := (*[syscall.PathMax]byte)(unsafe.Pointer(&eventbuf[offset+syscall.SizeofInotifyEvent]))
			name := string(bytes.TrimRight(namebytes[0:namelen], "\000"))
			dir, ok := watches[wd]
			if !ok {
				log.Fatalf("unknown watch descriptor %d.", wd)
			}
			name = path.Join(dir, name)
			if mask&syscall.IN_IGNORED != 0 {
				delete(watches, wd)
			}
			if mask&syscall.IN_CREATE != 0 || mask&syscall.IN_MOVED_TO != 0 {
				fi, err := os.Stat(name)
				if err == nil && fi.IsDir() {
					watchpath(name)
				}
			}
			filech <- name
			offset += syscall.SizeofInotifyEvent + namelen
		}
	}
}

func warn() {
	os.Stdout.WriteString("\a")
	if len(*warncmdFlag) == 0 {
		return
	}
	args := strings.Fields(*warncmdFlag)
	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Run(); err != nil {
		log.Printf("couldn't execute -warncmd: %v", err)
	}
}

func namePart(s string) string {
	return path.Dir(s)
}

func shasumPart(s string) string {
	return path.Base(s)
}

func (gs *gdsnap) listfiles() {
	if len(gs.accesstoken) == 0 {
		gs.gettoken()
	}

	files := map[string]fileinfo{}
	type listResponseType struct {
		IncompleteSearch bool
		NextPageToken    string
		Files            []fileinfo
	}

	// list existing files.
	q := url.Values{}
	q.Set("fields", "files(name,id,size,mimeType,modifiedTime,trashed,properties),nextPageToken,incompleteSearch")
	q.Set("pageSize", "1000")
	q.Set("q", fmt.Sprintf("'%s' in parents and properties has {key='gdsnap.profile' and value='%s'}", *gdirFlag, *profileFlag))
	for {
		listreq, err := http.NewRequest("GET", "https://www.googleapis.com/drive/v3/files?"+q.Encode(), nil)
		if err != nil {
			log.Fatal(err)
		}
		listreq.Header.Set("Accept", "application/json")
		listreq.Header.Set("Authorization", "Bearer "+gs.accesstoken)
		listresp, err := http.DefaultClient.Do(listreq)
		if err != nil {
			log.Fatalf("error listing files: %v", err)
		}
		listbody, err := io.ReadAll(listresp.Body)
		if err != nil {
			log.Fatalf("couldn't list everything: %v", err)
		}
		var r listResponseType
		if err = json.Unmarshal(listbody, &r); err != nil {
			log.Fatalf("couldn't parse list response: %v\nbody:\n%s", err, listbody)
		}
		if r.IncompleteSearch {
			log.Fatal("response was incomplete.")
		}
		for _, f := range r.Files {
			files[namePart(f.Name)] = f
		}
		if len(r.NextPageToken) == 0 {
			break
		}
		q.Set("pageToken", r.NextPageToken)
	}
	gs.files = files
}

func (gs *gdsnap) init() {
	if len(*ignoreFlag) > 0 {
		gs.ignore = strings.Split(*ignoreFlag, ",")
	}

	key := argon2.IDKey([]byte("tmc4~tyőDKßVWaSa"), []byte(*passwordFlag), 1, 64<<10, 4, chacha20poly1305.KeySize)
	var err error
	gs.aead, err = chacha20poly1305.NewX(key)
	if err != nil {
		log.Fatalf("gdsnap.CreateChachaCipher: %v", err)
	}
}

type gfileProperties struct {
	Name         string            `json:"name,omitempty"`
	Parents      []string          `json:"parents,omitempty"`
	Properties   map[string]string `json:"properties,omitempty"`
	ModifiedTime string            `json:"modifiedTime,omitempty"`
	Trashed      *bool             `json:"trashed,omitempty"`
}

func (gs *gdsnap) savepath(abspath string, verbose bool) {
	if !strings.HasPrefix(abspath, *dirFlag) {
		log.Printf("skipping %s because it's not under %s.", abspath, *dirFlag)
		return
	}
	relpath, ignore := abspath[len(*dirFlag):], false
	for _, ign := range gs.ignore {
		if matchglob(ign, relpath) {
			ignore = true
			break
		}
	}

	fi, exist := gs.files[relpath]
	if ignore && (!exist || fi.Trashed) {
		return
	}

	if exist && fi.ID == "" {
		// This can happen only in a rare race condition.
		// The ID field is only filled in the main loop's gs.listfiles() function.
		// If empty then this file was created in this cycle and then visited again through a recursive call.
		// Most of the time nothing needs to be done because the file is already uploaded.
		// In rare cases the file's content might have changed between now and its creation in the current cycle.
		// No need to do anything in that case either because the next main cycle will deal with that change.
		// In my next life I should avoid inotify and these ugly recursions and just do a full cycle each iteration.
		return
	}

	finfo, err := os.Lstat(abspath)
	if err != nil && (!exist || fi.Trashed) {
		// a path that cannot be statted and is not on gdrive either?
		// this might be a deleted or moved directory since those are not uploaded.
		// check any files rooted under this name just in case.
		// note: this might be slow when deleting many directories.
		// consider optimizing in that case in some way.
		// e.g. listfiles could precompute a dir->[file] map.
		this := relpath + "/"
		for p := range gs.files {
			if strings.HasPrefix(p, this) {
				gs.savepath(path.Join(*dirFlag, p), verbose)
			}
		}
		return
	}

	var contents []byte
	var needTrashing bool
	var modtime string
	var shasumstr string
	newfi := fi
	if err != nil || ignore {
		needTrashing = true
		newfi.Trashed = true
		newfi.MimeType = "gdsnap/deleted"
	} else {
		newfi.Trashed = false
		newfi.MimeType = fmt.Sprintf("gdsnap/data%03o", finfo.Mode().Perm())

		// save all files under a directory if abspath is a directory.
		if finfo.IsDir() {
			walk := func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					log.Printf("filepath.WalkDir(%s) error: %v", path, err)
				}
				if !d.IsDir() {
					gs.savepath(path, verbose)
				}
				return nil
			}
			if err := filepath.WalkDir(abspath, walk); err != nil {
				log.Printf("couldn't fully walk %s: %v", relpath, err)
			}
			return
		}

		modtime = finfo.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
		newfi.ModifiedTime = modtime
		if !fi.Trashed && modtime == fi.ModifiedTime {
			return
		}
		if finfo.Mode().Type() == fs.ModeSymlink {
			symlink, err := os.Readlink(abspath)
			if err != nil {
				log.Printf("skipping %s because couldn't read the symlink: %v", relpath, err)
			}
			newfi.MimeType = "gdsnap/symlink"
			contents = []byte(symlink)
		} else if !finfo.Mode().IsRegular() {
			log.Printf("skipping %s because it's not a regular file.", relpath)
			return
		} else {
			if finfo.Size() > int64(*sizelimitmbFlag)*1e6 {
				log.Printf("[warning] skipping %s because it's too big (%d MB); bump --sizelimit or ignore.", relpath, finfo.Size()/1e6)
				warn()
				return
			}
			rawcontents, err := os.ReadFile(abspath)
			if err != nil {
				log.Printf("[warning] couldn't load %s: %v", relpath, err)
				warn()
				return
			}

			// compress and encrypt the file.
			compressed := &bytes.Buffer{}
			compressor, err := flate.NewWriter(compressed, 9)
			if err != nil {
				log.Fatalf("couldn't create compressor %s: %v", relpath, err)
			}
			if n, err := compressor.Write(rawcontents); n != len(rawcontents) || err != nil {
				log.Fatalf("couldn't compress %s: %v", relpath, err)
			}
			if err := compressor.Close(); err != nil {
				log.Fatalf("couldn't close compressor for %s: %v", relpath, err)
			}
			nonce := make([]byte, gs.aead.NonceSize())
			if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
				log.Fatalf("gdsnap.ReadNewNonce: %v", err)
			}
			contents = gs.aead.Seal(nonce, nonce, compressed.Bytes(), nil)

			// Skip if sha256sum already matches.
			shasum := sha256.Sum256(rawcontents)
			shasumstr = hex.EncodeToString(shasum[:])
			newfi.Size = strconv.Itoa(len(contents))
			if !fi.Trashed && newfi.Size == fi.Size && shasumstr == shasumPart(fi.Name) {
				return
			}
		}
	}

	gs.gettoken()
	ct := gfileProperties{}
	if !exist {
		ct = gfileProperties{
			Name:       relpath + "/" + shasumstr,
			Parents:    []string{*gdirFlag},
			Properties: map[string]string{"gdsnap.profile": *profileFlag},
		}
	} else {
		ct = gfileProperties{
			Name:    relpath + "/" + shasumstr,
			Trashed: &needTrashing,
		}
	}
	if !needTrashing {
		ct.ModifiedTime = modtime
	}
	createData, err := json.Marshal(ct)
	if err != nil {
		log.Fatalf("couldn't create properties for %s: %v", relpath, err)
	}

	if len(contents) > 5e6 {
		// large files must be uploaded using 2 separate requests.
		var startReq *http.Request
		if exist {
			startReq, err = http.NewRequest("PATCH", "https://www.googleapis.com/upload/drive/v3/files/"+fi.ID+"?uploadType=resumable", bytes.NewReader(createData))
		} else {
			startReq, err = http.NewRequest("POST", "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable", bytes.NewReader(createData))
		}
		if err != nil {
			log.Fatalf("couldn't create largefile upload request for %s: %v", relpath, err)
		}
		startReq.Header.Set("Authorization", "Bearer "+gs.accesstoken)
		startReq.Header.Set("Content-Type", "application/json; charset=UTF-8")
		startReq.Header.Set("X-Upload-Content-Type", newfi.MimeType)
		startReq.Header.Set("X-Upload-Content-Length", strconv.Itoa(len(contents)))
		startResp, err := http.DefaultClient.Do(startReq)
		if err != nil {
			log.Fatalf("largefile upload start for %s failed: %v", relpath, err)
		}
		if startResp.StatusCode != 200 {
			body, _ := io.ReadAll(startResp.Body)
			log.Fatalf("largefile upload start for %s returned error: %s\n%s", relpath, startResp.Status, body)
		}
		loc := startResp.Header.Get("Location")
		if len(loc) == 0 {
			log.Fatalf("missing upload location when uploading %s", relpath)
		}

		uploadReq, err := http.NewRequest("PUT", loc, bytes.NewReader(contents))
		if err != nil {
			log.Fatalf("couldn't create largefile upload start request for %s: %v.", relpath, err)
		}
		uploadResp, err := http.DefaultClient.Do(uploadReq)
		if err != nil {
			log.Fatalf("largefile upload for %s failed: %v.", relpath, err)
		}
		if startResp.StatusCode != 200 {
			body, _ := io.ReadAll(startResp.Body)
			log.Fatalf("largefile upload for %s returned error: %s\n%s", relpath, uploadResp.Status, body)
		}
		log.Printf("%s uploaded (was a large file).", relpath)
		return
	}

	reqBuf := &bytes.Buffer{}
	w := multipart.NewWriter(reqBuf)
	mimeHeader := textproto.MIMEHeader{}
	mimeHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metadataWriter, err := w.CreatePart(mimeHeader)
	if err != nil {
		log.Fatalf("failed %s because couldn't create metadata part: %v", relpath, err)
	}
	metadataWriter.Write(createData)
	mimeHeader.Set("Content-Type", newfi.MimeType)
	contentWriter, err := w.CreatePart(mimeHeader)
	if err != nil {
		log.Fatalf("failed %s because couldn't create content part: %v", relpath, err)
	}
	contentWriter.Write(contents)
	w.Close()

	var createReq *http.Request
	var kind string
	if exist {
		createReq, err = http.NewRequest("PATCH", "https://www.googleapis.com/upload/drive/v3/files/"+fi.ID+"?uploadType=multipart", bytes.NewReader(reqBuf.Bytes()))
		kind = "existing"
	} else {
		createReq, err = http.NewRequest("POST", "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart", bytes.NewReader(reqBuf.Bytes()))
		kind = "new"
	}
	if err != nil {
		log.Fatalf("couldn't create upload/create request for %s: %v", relpath, err)
	}
	createReq.Header.Set("Authorization", "Bearer "+gs.accesstoken)
	createReq.Header.Set("Accept", "application/json")
	createReq.Header.Set("Content-Type", "multipart/related; boundary="+w.Boundary())
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		log.Fatalf("upload of %s failed: %v.", relpath, err)
	}
	createBody, err := io.ReadAll(createResp.Body)
	if err != nil {
		log.Fatalf("couldn't read create response for %s: %v", relpath, err)
	}
	if createResp.StatusCode != 200 {
		log.Fatalf("upload of %s %s failed with %q:\n%s", kind, relpath, createResp.Status, createBody)
	}
	gs.files[relpath] = newfi
	if exist {
		log.Printf("%s updated.", relpath)
	} else {
		log.Printf("%s created.", relpath)
	}
}

func (gs *gdsnap) subcommandWatch(args []string) {
	if len(args) != 0 {
		log.Fatalf("error: unexpected cmdline arguments.")
	}
	gs.gettoken()
	gs.checkQuota()

	log.Print("initial scan: updating/deleting files seen on gdrive.")
	gs.listfiles()
	for _, f := range gs.files {
		if !f.Trashed {
			gs.savepath(path.Join(*dirFlag, f.Name), true)
		}
	}
	log.Print("initial scan: creating new files seen on disk.")
	gs.savepath(*dirFlag, true)

	filech := make(chan string, 1000)
	go watchdir(filech)

	// handle the initial scan from the inotify watch.
	log.Print("waiting for the changes to subside for a moment.")
initloop:
	for {
		select {
		case fn := <-filech:
			gs.savepath(fn, true)
		default:
			select {
			case fn := <-filech:
				gs.savepath(fn, true)
			case <-time.After(3 * time.Second):
				break initloop
			}
		}
	}

	sigintch := make(chan os.Signal, 2)
	signal.Notify(sigintch, syscall.SIGINT)
	runtime.GC()

	log.Print("main loop started: will track changed files and periodically upload them.")
	timer := time.NewTimer(*cycledurFlag)
	touched := map[string]bool{}
	for {
		wasSIGINT := false
		select {
		case fn := <-filech:
			touched[fn] = true
			continue
		case <-timer.C:
		case <-sigintch:
			wasSIGINT = true
			if len(touched) == 0 {
				log.Printf("got a sigint but skipping backup cycle since nothing changed (use sigquit to quit).")
			} else {
				log.Printf("got a sigint, running a backup cycle for %d files (use sigquit to quit).", len(touched))
			}
		}

		if len(touched) > 0 {
			gs.gettoken()
			gs.checkQuota()
			gs.listfiles()
			for fn := range touched {
				delete(touched, fn)
				gs.savepath(fn, false)
			}
			if wasSIGINT {
				log.Printf("backup cycle done.")
			}
			// it's the perfect time to collect the garbage from this cycle.
			runtime.GC()
		}
		timer.Stop()
		timer.Reset(*cycledurFlag)
	}
}

func (gs *gdsnap) subcommandAuth(args []string) {
	if len(args) != 0 {
		log.Fatalf("error: unexpected cmdline arguments.")
	}
	if len(*refreshtokenFlag) > 0 {
		log.Fatal(`-refreshtoken already defined. use "gdsnap -refreshtoken= auth" to force auth regardless.`)
	}

	q := url.Values{}
	q.Set("client_id", oaClientID)
	q.Set("scope", "https://www.googleapis.com/auth/drive.file")
	q.Set("response_type", "code")
	q.Set("redirect_uri", "http://127.0.0.1:1")
	u := "https://accounts.google.com/o/oauth2/auth?" + q.Encode()
	fmt.Println("visit and authorize gdsnap:")
	fmt.Println(u)
	fmt.Println("after the authorization it'll redirect to an invalid address.")
	fmt.Println("but from the url copy the code component's value here (starts with 4/):")

	var authcode string
	if _, err := fmt.Scan(&authcode); err != nil {
		log.Fatalf("authcode parse error: %v", err)
	}

	q = url.Values{}
	q.Set("code", authcode)
	q.Set("client_id", oaClientID)
	q.Set("client_secret", oaSecret)
	q.Set("redirect_uri", "http://127.0.0.1:1")
	q.Set("grant_type", "authorization_code")
	response, err := http.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded", strings.NewReader(q.Encode()))
	if err != nil {
		log.Fatal(err)
	}
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}
	response.Body.Close()

	var r map[string]interface{}
	if err = json.Unmarshal(responseBody, &r); err != nil {
		log.Fatal(err)
	}
	rt, ok := r["refresh_token"].(string)
	if !ok {
		log.Fatalf("couldn't parse refresh_token from response. full response:\n%s", string(responseBody))
	}
	fmt.Println("add the following to ~/.gdsnap, ~/.config/gdsnap, or ~/.cache/gdsnap (this is a sensitive token, don't share this!):")
	fmt.Printf("* refreshtoken %q\n", rt)
}

func (gs *gdsnap) subcommandList(args []string) {
	gs.listfiles()
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	q := url.Values{}
	q.Set("fields", "revisions(originalFilename,id,size,modifiedTime,mimeType)")
	q.Set("pageSize", "1000")
	for _, f := range filterfiles(gs.files, args) {
		fi := gs.files[f]
		fmt.Fprintf(out, "%+v\n", fi)
		req, err := http.NewRequest("GET", "https://www.googleapis.com/drive/v3/files/"+fi.ID+"/revisions?"+q.Encode(), nil)
		if err != nil {
			log.Fatalf("couldn't create request for %s: %v", f, err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+gs.accesstoken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("error listing revisions for %s: %v", f, err)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("error reading revisions for %s: %v", f, err)
		}
		fmt.Fprintln(out, string(body))
		fmt.Fprintln(out)
	}
}

func (gs *gdsnap) subcommandSave(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: gdsnap [flags...] save [file...]")
		return
	}
	gs.listfiles()

	for _, f := range args {
		fullpath, err := filepath.Abs(f)
		if err != nil {
			log.Fatalf("couldn't create absolute path for %s: %s", f, err)
		}
		if fullpath == (*dirFlag)[:len(*dirFlag)-1] {
			fullpath += "/"
		}
		gs.savepath(fullpath, true)
	}
}

func (gs *gdsnap) decrypt(fi *fileinfo, mime string, content []byte) (string, []byte) {
	if !strings.HasPrefix(mime, "gdsnap/data") {
		return mime, content
	}
	if len(content) < gs.aead.NonceSize() {
		log.Printf("gdsnap.ContentTooShortToDecrypt name=%s got=%d want=%d", namePart(fi.Name), len(content), gs.aead.NonceSize())
		return mime, content
	}
	// Decrypt and decompress the file.
	compressed, err := gs.aead.Open(nil, content[:gs.aead.NonceSize()], content[gs.aead.NonceSize():], nil)
	if err != nil {
		log.Printf("gdsnap.OpenEncryptedContent name=%s: %s", namePart(fi.Name), err)
		return mime, content
	}
	decompressor := flate.NewReader(bytes.NewBuffer(compressed))
	if content, err = io.ReadAll(decompressor); err != nil {
		log.Printf("gdsnap.Decompress name=%s: %s", namePart(fi.Name), err)
		return mime, content
	}
	if err = decompressor.Close(); err != nil {
		log.Printf("gdsnap.CloseDecompressor name=%s: %s", namePart(fi.Name), err)
	}
	return mime, content
}

// revfetch fetches the content at a specific version.
// the content fetching is skipped if the revision's last modified time equals to skipDate.
func (gs *gdsnap) revfetch(fi *fileinfo, skipDate string) (mime string, content []byte) {
	if len(*tFlag) == 0 || fi.ModifiedTime <= *tFlag {
		if fi.ModifiedTime == skipDate {
			return fi.MimeType, nil
		}
		getreq, err := http.NewRequest("GET", "https://www.googleapis.com/drive/v3/files/"+fi.ID+"?alt=media", nil)
		if err != nil {
			log.Fatal(err)
		}
		getreq.Header.Set("Authorization", "Bearer "+gs.accesstoken)
		getresp, err := http.DefaultClient.Do(getreq)
		if err != nil {
			log.Fatalf("error fetching contents for %s: %v", namePart(fi.Name), err)
		}
		contents, err := io.ReadAll(getresp.Body)
		if err != nil {
			log.Fatalf("error reading contents for %s: %v", namePart(fi.Name), err)
		}
		return gs.decrypt(fi, fi.MimeType, contents)
	}

	type revinfo struct {
		ID           string
		MimeType     string
		ModifiedTime string
	}

	q := url.Values{}
	q.Set("fields", "revisions(originalFilename,id,size,modifiedTime,mimeType)")
	q.Set("pageSize", "1000")
	req, err := http.NewRequest("GET", "https://www.googleapis.com/drive/v3/files/"+fi.ID+"/revisions?"+q.Encode(), nil)
	if err != nil {
		log.Fatalf("couldn't create request for %s: %v", namePart(fi.Name), err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+gs.accesstoken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("error listing revisions for %s: %v", namePart(fi.Name), err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("error reading revisions for %s: %v", namePart(fi.Name), err)
	}
	var revisionsResponse struct {
		Revisions []revinfo
	}
	if err = json.Unmarshal(body, &revisionsResponse); err != nil {
		log.Fatalf("couldn't parse revisions response: %v\nbody:\n%s", err, body)
	}

	var ri *revinfo
	for i, r := range revisionsResponse.Revisions {
		if r.ModifiedTime > *tFlag {
			continue
		}
		if ri == nil || r.ModifiedTime > ri.ModifiedTime {
			ri = &revisionsResponse.Revisions[i]
		}
	}
	if ri == nil {
		return "gdsnap/deleted", nil
	}
	if ri.ModifiedTime == skipDate {
		return ri.MimeType, nil
	}
	getreq, err := http.NewRequest("GET", "https://www.googleapis.com/drive/v3/files/"+fi.ID+"/revisions/"+ri.ID+"?alt=media", nil)
	if err != nil {
		log.Fatal(err)
	}
	getreq.Header.Set("Authorization", "Bearer "+gs.accesstoken)
	getresp, err := http.DefaultClient.Do(getreq)
	if err != nil {
		log.Fatalf("error fetching contents for %s: %v", namePart(fi.Name), err)
	}
	contents, err := io.ReadAll(getresp.Body)
	if err != nil {
		log.Fatalf("error reading contents for %s: %v", namePart(fi.Name), err)
	}
	return gs.decrypt(fi, ri.MimeType, contents)
}

func (gs *gdsnap) subcommandCat(args []string) {
	gs.listfiles()
	for _, relpath := range filterfiles(gs.files, args) {
		fi := gs.files[relpath]
		mime, contents := gs.revfetch(&fi, "")
		switch mime {
		case "gdsnap/deleted":
			fmt.Printf("%s is deleted.", relpath)
		case "gdsnap/symlink":
			fmt.Printf("%s is a symlink to %s.", relpath, contents)
		default:
			os.Stdout.Write(contents)
		}
	}
}

func (gs *gdsnap) subcommandDiff(args []string) {
	gs.listfiles()
	for _, relpath := range filterfiles(gs.files, args) {
		fi, ok := gs.files[relpath]
		if !ok {
			log.Fatalf("%s is not backed up.", relpath)
		}

		fullpath, err := filepath.Abs(filepath.Join(*dirFlag, relpath))
		finfo, err := os.Lstat(fullpath)
		if err != nil {
			mime, _ := gs.revfetch(&fi, fi.ModifiedTime)
			if mime != "gdsnap/deleted" {
				fmt.Printf("skipping %s because can't stat it: %v.\n", relpath, err)
			}
			continue
		}
		const tLayout = "2006-01-02T15:04:05.000Z"
		fileDate := finfo.ModTime().UTC().Format(tLayout)
		mime, contents := gs.revfetch(&fi, fileDate)
		if mime == "gdsnap/deleted" {
			fmt.Printf("skipping %s because because it's trashed in the archive.\n", relpath)
			continue
		}
		if contents == nil {
			// revfetch returns nil if the revision's mod time is fileDate.
			// it means there wasn't any diff so let's further diffing.
			continue
		}
		filetype := "regular file"
		if finfo.Mode().Type() == fs.ModeSymlink {
			filetype = "symlink"
		} else if !finfo.Mode().IsRegular() {
			fmt.Printf("skipping %s because it's not a regular file.\n", relpath)
			continue
		}

		backuptype := "regular file"
		if mime == "gdsnap/symlink" {
			backuptype = "symlink"
		}

		if filetype != backuptype {
			fmt.Printf("%s differs in type: -%s vs +%s.\n", relpath, backuptype, filetype)
			continue
		}

		if backuptype == "symlink" {
			symlink, err := os.Readlink(fullpath)
			if err != nil {
				log.Printf("can't read symlink for %s: %v", relpath, err)
				continue
			}
			strcontents := string(contents)
			if strcontents != symlink {
				fmt.Printf("--- archive/%s\n+++ archive/%s\n-%s\n+%s\n", relpath, relpath, strcontents, symlink)
			}
			continue
		}

		cmd := exec.Command("diff", "-u", "--label=archive/"+relpath, "--label=current/"+relpath, "-", fullpath)
		cmd.Stdin = bytes.NewReader(contents)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}
}

func (gs *gdsnap) subcommandRestore(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: gdsnap [flags] restore [globs...]")
		return
	}
	gs.listfiles()
	for _, relpath := range filterfiles(gs.files, args) {
		fullpath, err := filepath.Abs(filepath.Join(*dirFlag, relpath))
		os.Remove(fullpath)
		os.MkdirAll(filepath.Dir(fullpath), 0755)
		fi, ok := gs.files[relpath]
		if !ok {
			fmt.Printf("skipping %s because it's not backed up.", relpath)
			continue
		}
		mime, contents := gs.revfetch(&fi, "")
		if mime == "gdsnap/deleted" {
			continue
		}

		if mime == "gdsnap/symlink" {
			if err = os.Symlink(string(contents), fullpath); err != nil {
				log.Printf("couldn't symlink %s: %v", relpath, err)
				continue
			}
		} else {
			var perm fs.FileMode = 0600
			fmt.Sscanf(mime, "gdsnap/data%o", &perm)
			if err = os.WriteFile(fullpath, contents, perm); err != nil {
				log.Printf("couldn't write %s: %v", relpath, err)
				continue
			}
		}
		log.Printf("successfully restored %s", relpath)
	}
}

type quota struct {
	UsageMB, LimitMB, FreeMB, DriveMB, TrashMB int64
}

func (gs *gdsnap) getquota() quota {
	req, err := http.NewRequest("GET", "https://www.googleapis.com/drive/v3/about?fields=storageQuota", nil)
	if err != nil {
		log.Fatalf("error creating quota request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+gs.accesstoken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("failed querying quota data: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("quota request returned error: %s\n%s", resp.Status, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("failed reading quota data: %v", err)
	}
	var about struct {
		StorageQuota struct {
			Usage, Limit, UsageInDrive, UsageInDriveTrash string
		}
	}
	if err = json.Unmarshal(body, &about); err != nil {
		log.Fatalf("couldn't parse quota response: %v, body:\n%s", err, body)
	}
	q := quota{}
	fmt.Sscan(about.StorageQuota.Usage, &q.UsageMB)
	fmt.Sscan(about.StorageQuota.Limit, &q.LimitMB)
	fmt.Sscan(about.StorageQuota.UsageInDrive, &q.DriveMB)
	fmt.Sscan(about.StorageQuota.UsageInDriveTrash, &q.TrashMB)
	q.FreeMB = (q.LimitMB - q.UsageMB) / 1e6
	q.LimitMB += 1e6 - 1
	q.UsageMB += 1e6 - 1
	q.DriveMB += 1e6 - 1
	q.TrashMB += 1e6 - 1
	q.LimitMB /= 1e6
	q.UsageMB /= 1e6
	q.DriveMB /= 1e6
	q.TrashMB /= 1e6
	return q
}

func (gs *gdsnap) checkQuota() {
	q := gs.getquota()
	if q.FreeMB < 4000 {
		log.Printf("[warning] remaining quota too low: %d MB.", q.FreeMB)
		warn()
	}
}

func (gs *gdsnap) subcommandQuota(args []string) {
	if len(args) != 0 {
		fmt.Println("usage: gdsnap [flags] quota")
		fmt.Println("no arguments allowed.")
	}
	gs.gettoken()
	q := gs.getquota()
	fmt.Printf("limit: %5d MB\nusage: %5d MB\ndrive: %5d MB\ntrash: %5d MB\nfree:  %5d MB\n", q.LimitMB, q.UsageMB, q.DriveMB, q.TrashMB, q.FreeMB)
}

func (gs *gdsnap) gettoken() {
	if len(*refreshtokenFlag) == 0 {
		fmt.Println("missing -refreshtoken flag.")
		os.Exit(2)
		return
	}

	now := time.Now()
	if now.Sub(gs.tokenbirth) < 50*time.Minute {
		return
	}
	gs.tokenbirth = now

	q := url.Values{}
	q.Set("client_id", oaClientID)
	q.Set("client_secret", oaSecret)
	q.Set("refresh_token", *refreshtokenFlag)
	q.Set("grant_type", "refresh_token")
	response, err := http.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded", strings.NewReader(q.Encode()))
	if err != nil {
		log.Fatal(err)
	}
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}
	var r map[string]interface{}
	if err = json.Unmarshal(responseBody, &r); err != nil {
		log.Fatal("couldn't acquire access token: ", err)
	}
	accesstoken, ok := r["access_token"].(string)
	if !ok {
		log.Fatalf("couldn't parse access_token from response. run `gdsnap auth`? full response:\n%s", string(responseBody))
	}
	gs.accesstoken = accesstoken
}

func readconfig() {
	overridden := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { overridden[f.Name] = true })

	for _, cfgfile := range []string{".gdsnap", ".config/gdsnap", ".cache/gdsnap"} {
		contents, err := os.ReadFile(path.Join(os.Getenv("HOME"), cfgfile))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			log.Fatal(err)
		}
		for _, line := range strings.Split(string(contents), "\n") {
			line = strings.TrimSpace(line)
			if len(line) == 0 || line[0] == '#' {
				continue
			}
			var matcher, flagname, value string
			if _, err := fmt.Sscanf(line, "%s %s %q", &matcher, &flagname, &value); err != nil {
				log.Fatalf("%s: invalid config line %q", cfgfile, line)
				continue
			}
			if matcher != "*" && matcher != *profileFlag || overridden[flagname] {
				continue
			}
			f := flag.Lookup(flagname)
			if f == nil {
				log.Fatalf("%s: unknown flag %s", cfgfile, flagname)
			}
			if err := f.Value.Set(value); err != nil {
				log.Fatalf("%s: can't set flag %s: %v", cfgfile, flagname, err)
			}
		}
	}
}

func Run(ctx context.Context) error {
	go func() {
		sigquitch := make(chan os.Signal, 1)
		signal.Notify(sigquitch, syscall.SIGQUIT)
		<-sigquitch
		log.Print("sigquit received, quitting.")
		os.Exit(1)
	}()

	log.SetFlags(log.Flags() | log.Lshortfile)

	flag.Usage = usage
	flag.Parse()
	readconfig()

	subcommand := flag.Arg(0)
	if len(subcommand) == 0 {
		usage()
		return nil
	}
	args := flag.Args()[1:]
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			log.Printf("the flaglike argument %q in a non-flag position. if it's a flag, it won't have an effect.", a)
		}
	}

	if !strings.HasSuffix(*dirFlag, "/") {
		*dirFlag += "/"
	}

	if len(*tFlag) > 0 {
		const tLayout = "2006-01-02T15:04:05.000Z"
		var t time.Time
		var absErr error
		dur, durErr := time.ParseDuration(*tFlag)
		if durErr == nil {
			t = time.Now().Add(-dur).UTC()
		} else {
			if len(*tFlag) == 4 { // 2022
				*tFlag += "-01"
			}
			if len(*tFlag) == 7 { // 2022-01
				*tFlag += "-01"
			}
			if len(*tFlag) == 10 { // 2022-01-01
				*tFlag += "T00"
			}
			if len(*tFlag) == 13 { // 2022-01-01T00
				*tFlag += ":00"
			}
			if len(*tFlag) == 16 { // 2022-01-01T00:00
				*tFlag += ":00"
			}
			if len(*tFlag) == 19 { // 2022-01-01T00:00:00
				*tFlag += ".000"
			}
			if len(*tFlag) == 23 { // 2022-01-01T00:00:00.000
				*tFlag += "Z"
			}
			t, absErr = time.Parse(tLayout, *tFlag)
		}
		if durErr != nil && absErr != nil {
			log.Fatalf("can't parse %q as duration (%v) nor as absolute time (%v)", *tFlag, durErr, absErr)
		}
		*tFlag = t.Format(tLayout)
	}

	gs := gdsnap{}
	gs.init()

	switch subcommand {
	case "auth":
		gs.subcommandAuth(args)
	case "cat":
		gs.subcommandCat(args)
	case "diff":
		gs.subcommandDiff(args)
	case "help":
		usage()
	case "list":
		gs.subcommandList(args)
	case "quota":
		gs.subcommandQuota(args)
	case "restore":
		gs.subcommandRestore(args)
	case "save":
		gs.subcommandSave(args)
	case "watch":
		gs.subcommandWatch(args)
	default:
		log.Fatalf("error: unrecognized subcommand %q.", subcommand)
	}

	return nil
}
