package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if len(os.Args) >= 2 {
		fmt.Print(`google authenticator compatible otp generator.

configure by adding the otp urls to the ~/.config/.otps file.
parse a qr code by saving the image to disk and then use https://nimiq.github.io/qr-scanner/demo/ on it.
test otp generation via comparing it to http://blog.tinisles.com/2011/10/google-authenticator-one-time-password-algorithm-in-javascript/.
example url: otpauth://totp/MYSERVICE:myusername?algorithm=SHA1&issuer=MYSERVICE&secret=JBSWY3DPEHPK3PXP&digits=6&period=30.
inspired by https://github.com/pcarrier/gauth.
`)
		return
	}

	// fetch the accurate time from an ntp server.
	conn, err := net.Dial("udp", "89.234.64.77:123")
	if err != nil {
		log.Fatal(err)
	}
	sntp := [60]byte{}
	sntp[0] = 19
	if _, err = conn.Write(sntp[:]); err != nil {
		log.Fatal(err)
	}
	var rby int
	if rby, err = conn.Read(sntp[:]); err != nil {
		log.Fatal(err)
	}
	conn.Close()
	if rby < 44 {
		log.Fatalf("sntp read too short: got:%d, want:%d", rby, 44)
	}
	var ts int64
	ts |= int64(sntp[40])<<24 | int64(sntp[41])<<16 | int64(sntp[42])<<8 | int64(sntp[43])
	ts -= (70*365 + 17) * 86400

	// read the config file.
	cfgfile, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config/.otps"))
	if err != nil {
		log.Fatal(err)
	}
	cfg := bytes.NewReader(cfgfile)

	// print the otp for each config entry.
	for {
		// parse the url. example url:
		// otpauth://totp/MYSERVICE:myusername?algorithm=SHA1&issuer=MYSERVICE&secret=JBSWY3DPEHPK3PXP&digits=6&period=30
		var cfgentry string
		if cnt, _ := fmt.Fscan(cfg, &cfgentry); cnt == 0 {
			break
		}
		cfgurl, err := url.Parse(cfgentry)
		if err != nil {
			log.Fatal(err)
		}
		v := cfgurl.Query()
		var digits, period int
		digits, _ = strconv.Atoi(v.Get("digits"))
		period, _ = strconv.Atoi(v.Get("period"))
		if !v.Has("secret") || v.Get("algorithm") != "SHA1" || digits == 0 || period == 0 {
			log.Printf("unsupported url %q", cfgurl.Path)
		}

		// create the hasher.
		key, err := base32.StdEncoding.DecodeString(v.Get("secret"))
		if err != nil {
			log.Print(err)
			continue
		}
		h := hmac.New(sha1.New, key)

		// hash the current timestamp.
		msg := [8]byte{}
		binary.BigEndian.PutUint64(msg[:], uint64(ts/int64(period)))
		h.Write(msg[:])
		digest := h.Sum(nil)

		// extract the code.
		offset := digest[len(digest)-1] & 0x0f
		code := (uint64(digest[offset]&0x7f) << 24) |
			(uint64(digest[offset+1]) << 16) |
			(uint64(digest[offset+2]) << 8) |
			(uint64(digest[offset+3]) << 0)

		// format the code.
		if digits > 10 {
			log.Printf("%s: too many digits needed!", cfgurl.Path[1:])
			continue
		}
		codestr := fmt.Sprintf("%010d", code)
		codestr = codestr[len(codestr)-digits:]
		fmt.Printf("%16s %s\n", cfgurl.Path[1:], codestr)
	}
}
