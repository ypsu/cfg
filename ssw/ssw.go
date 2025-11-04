// The tool ssw (Simple SWitch) sets hue and monitor brightness.
//
// Usage: ssw [abdhot0123]
// Example: ssw o0b2
//
// Prints current light levels if given no arguments.
package ssw

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	_ "embed"
)

// Lamp data from `huepush -dump`.
const data = `
a 8dcb6ab3-9588-4546-8bd2-a6f6cf52ac12 1106b05c-2576-4e64-8d3d-48a226fde983 b4b5ec3f-1ca0-4322-bf56-91fd670326f0 0c55f023-2f80-42ab-b992-1fb15a7895e3
b 187c1054-23bd-4fc1-bd4e-3aab8a6be753 29e6a967-3b11-4a8d-a9c6-e1405ae89064 2dba0620-8a84-4bb3-8c72-4f0a79e13fa2 f2d17b83-a190-45f0-8e44-002b617847c9
h 7f2af9aa-5403-43c8-a908-3d89096897de 090acc6d-4004-494e-ae8c-587498ce697f d3927402-90b8-498f-af1f-762dcac077a8 e70f9794-9942-4d9c-91c6-488dd279c997
o c69ee95f-0bbc-43c4-a823-9c3d74f4d5b8 434b05f3-6738-42d5-8f03-db248eae84ce 7b7d17c2-6761-4738-9552-9d999485e511 16099744-8eec-43f5-8901-f5871a440197
t 2407f461-df2a-4324-9f15-077d349e968b e2a5d992-dd8b-4391-9ae9-4be756bd0418 d3492c62-4919-413d-ae1e-ef596ee7e231 354de2b2-6fc8-4529-bd63-27f49e703335
`

//go:embed ssw.go
var source string

func usage() {
	for line := range strings.Lines(source) {
		if len(line) == 0 || line[0] != '/' {
			break
		}
		fmt.Fprint(flag.CommandLine.Output(), strings.TrimPrefix(strings.TrimPrefix(line, "//"), " "))
	}
	flag.PrintDefaults()
}

// bridgeAddress reads local configuration.
func bridgeAddress() (address, key string, err error) {
	var hueAddress, hueKey string
	cfgfile := filepath.Join(os.Getenv("HOME"), ".config/hue.cfg")
	cfgBytes, err := os.ReadFile(cfgfile)
	if err != nil {
		return "", "", fmt.Errorf("huepush.ReadLocalConfiguration: %v", err)
	}
	for line := range bytes.Lines(cfgBytes) {
		fields := strings.Fields(string(line))
		if len(fields) <= 1 {
			continue
		}
		if fields[0] == "hueaddress" {
			hueAddress = fields[1]
		} else if fields[0] == "huekey" {
			hueKey = fields[1]
		}
	}
	if hueAddress == "" || hueKey == "" {
		return "", "", fmt.Errorf("ssw.MissingBridgeData (either hueaddress or huekey is missing from $HOME/.config/hue.cfg)")
	}
	return hueAddress, hueKey, nil
}

func setDDCBrightness(brightness int) error {
	fd, err := os.OpenFile("/dev/i2c-12", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("ssw.OpenI2CFile: %v", err)
	}
	defer fd.Close()

	type i2cMessage struct {
		addr  uint16
		flags uint16
		len   uint16
		buf   uintptr
	}
	type i2cRDWRData struct {
		msgs  uintptr
		nmsgs uint32
	}
	payload := []byte{0x51, 0x84, 0x03, 0x10, 0x00, byte(brightness), 0x00}
	msg := i2cMessage{0x37, 0, uint16(len(payload)), uintptr(unsafe.Pointer(&payload[0]))}
	data := i2cRDWRData{uintptr(unsafe.Pointer(&msg)), 1}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd.Fd(), 0x0707, uintptr(unsafe.Pointer(&data)))
	if errno != 0 {
		return fmt.Errorf("ssw.Syscall: %v", errno)
	}
	return nil
}

func setBrightness(ctx context.Context, lamp byte, level int) error {
	if level > 3 {
		return fmt.Errorf("ssw.TargetTooHigh got=%d max=%d", level, 3)
	}

	// d means Display.
	if lamp == 'd' {
		levels := [...]int{0, 25, 75, 100}
		if err := setDDCBrightness(levels[level]); err != nil {
			return fmt.Errorf("ssw.SetBrightness: %v", err)
		}
		return nil
	}

	hueAddress, hueKey, err := bridgeAddress()
	if err != nil {
		return fmt.Errorf("ssw.BridgeAddressForBrightness: %v", err)
	}

	// Find the matching target.
	target := ""
	for line := range strings.Lines(data) {
		fields := strings.Fields(line)
		if len(fields) <= 2 || fields[0][0] != lamp {
			continue
		}
		if level >= len(fields)-1 {
			return fmt.Errorf("ssw.TargetHigherThanScenes got=%d max=%d", level, len(fields)-2)
		}
		target = fields[level+1]
		break
	}
	if target == "" {
		return fmt.Errorf("ssw.TargetNotFound target=%c", lamp)
	}

	// Craft and send the request.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resource, body := "/scene/", bytes.NewReader([]byte(`{"recall":{"action": "active"}}`))
	if level == 0 {
		resource, body = "/grouped_light/", bytes.NewReader([]byte(`{"on": {"on": false}}`))
	}
	setRequest, err := http.NewRequestWithContext(ctx, "PUT", hueAddress+"/clip/v2/resource"+resource+target, body)
	if err != nil {
		return fmt.Errorf("ssw.NewSetRequest: %v", err)
	}
	setRequest.Header.Set("Hue-Application-Key", hueKey)
	setResponse, err := client.Do(setRequest)
	if err != nil {
		return fmt.Errorf("ssw.SendSetRequest: %v", err)
	}
	setBody, err := io.ReadAll(setResponse.Body)
	if err != nil {
		setResponse.Body.Close()
		return fmt.Errorf("ssw.ReadSetResponseBody: %v", err)
	}
	setResponse.Body.Close()
	if setResponse.StatusCode != 200 {
		return fmt.Errorf("ssw.Set status=%q body:\n%s", setResponse.Status, setBody)
	}
	return nil
}

func jget(v any, path ...string) any {
	if len(path) == 0 {
		return v
	}
	if v == nil {
		return nil
	}
	if idx, err := strconv.Atoi(path[0]); err == nil { // on success
		return jget(v.([]any)[idx], path[1:]...)
	}
	return jget(v.(map[string]any)[path[0]], path[1:]...)
}

func printBrightness(ctx context.Context) error {
	hueAddress, hueKey, err := bridgeAddress()
	if err != nil {
		return fmt.Errorf("ssw.BridgeAddressForPrint: %v", err)
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	getRequest, err := http.NewRequestWithContext(ctx, "GET", hueAddress+"/clip/v2/resource/grouped_light", nil)
	if err != nil {
		return fmt.Errorf("ssw.NewGetRequest: %v", err)
	}
	getRequest.Header.Set("Hue-Application-Key", hueKey)
	getResponse, err := client.Do(getRequest)
	if err != nil {
		return fmt.Errorf("ssw.SendGetRequest: %v", err)
	}
	getBody, err := io.ReadAll(getResponse.Body)
	if err != nil {
		getResponse.Body.Close()
		return fmt.Errorf("ssw.ReadGetResponseBody: %v", err)
	}
	getResponse.Body.Close()
	if getResponse.StatusCode != 200 {
		return fmt.Errorf("ssw.GetGroupedLights status=%q body:\n%s", getResponse.Status, getBody)
	}

	brightness, response := map[string]float64{}, map[string]any{}
	if err := json.Unmarshal(getBody, &response); err != nil {
		return fmt.Errorf("ssw.ParseGroupedLights: %v", err)
	}
	for _, d := range jget(response, "data").([]any) {
		b := jget(d, "dimming", "brightness").(float64)
		if b < 1.0 && jget(d, "on", "on").(bool) {
			b = 1.0
		}
		brightness[jget(d, "id").(string)] = b
	}

	for line := range strings.Lines(data) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		b, ok := brightness[fields[1]]
		if ok { // on success
			fmt.Printf("%s %3.0f%%\n", fields[0], b)
		} else {
			fmt.Printf("%s unavailable\n", fields[0])
		}
	}
	return nil
}

func Run(ctx context.Context) error {
	// Parse arguments.
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() == 0 {
		if err := printBrightness(ctx); err != nil {
			return fmt.Errorf("ssw.PrintBrightness: %v", err)
		}
		return nil
	}

	targetLamp := byte('o')
	for _, arg := range flag.Args() {
		for _, c := range arg {
			if '0' <= c && c <= '9' {
				if err := setBrightness(ctx, targetLamp, int(c-'0')); err != nil {
					return fmt.Errorf("ssw.SetBrightness lamp=%c level=%d: %v", err, targetLamp, int(c-'0'))
				}
			} else {
				targetLamp = byte(c)
			}
		}
	}
	return nil
}
