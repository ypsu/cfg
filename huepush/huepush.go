// The tool huepush updates my Hue 4 button switches to the intent described in $HOME/.config/hue.cfg.
package huepush

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	_ "embed"
)

type ButtonID string
type ButtonIndex int
type RoomID string
type RoomName string
type SceneID string
type SceneName string
type SwitchID string
type SwitchName string

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

//go:embed huepush.go
var source string

func usage() {
	for line := range strings.Lines(source) {
		if len(line) == 0 || line[0] != '/' {
			break
		}
		fmt.Fprint(flag.CommandLine.Output(), strings.TrimPrefix(strings.TrimPrefix(line, "//"), " "))
	}
	fmt.Fprintln(flag.CommandLine.Output(), "\nFlags:")
	flag.PrintDefaults()
}

func Run(ctx context.Context) error {
	// Parse the flags.
	flagApply := flag.Bool("apply", false, "Apply the intent to the bridges.")
	flagDump := flag.Bool("dump", false, "Dump the configuration.")
	flag.Usage = usage
	flag.Parse()

	// Read local configuration.
	var hueAddress, hueKey string
	cfgfile := filepath.Join(os.Getenv("HOME"), ".config/hue.cfg")
	cfgBytes, err := os.ReadFile(cfgfile)
	if err != nil {
		return fmt.Errorf("huepush.ReadLocalConfiguration: %v", err)
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
		return fmt.Errorf("huepush.MissingBridgeData (either hueaddress or huekey is missing from $HOME/.config/hue.cfg)")
	}

	// Read the configuration from the bridge.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	configRequest, err := http.NewRequestWithContext(ctx, "GET", hueAddress+"/clip/v2/resource", nil)
	if err != nil {
		return fmt.Errorf("huepush.NewConfigRequest: %v", err)
	}
	configRequest.Header.Set("Hue-Application-Key", hueKey)
	configResponse, err := client.Do(configRequest)
	if err != nil {
		return fmt.Errorf("huepush.SendConfigRequest: %v", err)
	}
	configBody, err := io.ReadAll(configResponse.Body)
	if err != nil {
		configResponse.Body.Close()
		return fmt.Errorf("huepush.ReadConfigResponseBody: %v", err)
	}
	configResponse.Body.Close()
	if configResponse.StatusCode != 200 {
		return fmt.Errorf("huepush.FetchConfig status=%q body:\n%s", configResponse.Status, configBody)
	}
	var configJSON any
	if err := json.Unmarshal(configBody, &configJSON); err != nil {
		return fmt.Errorf("huepush.UnmarshalConfig: %v", err)
	}
	data := jget(configJSON, "data").([]any)

	// Set up the data structures.
	switches, switchIDs := map[SwitchID]*switchdesc{}, map[SwitchName]SwitchID{}
	roomNames, roomIDs := map[RoomID]RoomName{}, map[RoomName]RoomID{}
	roomScenes, sceneNames := map[RoomID][3]SceneID{}, map[SceneID]SceneName{}
	buttonIndexes := map[ButtonID]ButtonIndex{}
	levelNames := [3]SceneName{"low", "mid", "high"}

	// Extract the rooms.
	for _, d := range data {
		if jget(d, "type").(string) != "room" {
			continue
		}
		name, id := RoomName(jget(d, "metadata", "name").(string)), RoomID(jget(d, "id").(string))
		roomNames[id], roomIDs[name] = name, id
	}

	// Extract the switches.
	for _, d := range data {
		if jget(d, "type").(string) != "device" || jget(d, "product_data", "model_id") != "RWL022" {
			continue
		}
		switchName, switchID, buttonIndex := SwitchName(jget(d, "metadata", "name").(string)), SwitchID(jget(d, "id").(string)), ButtonIndex(0)
		switchIDs[switchName], switches[switchID] = switchID, &switchdesc{SwitchID: switchID, SwitchName: switchName}
		for _, s := range jget(d, "services").([]any) {
			if jget(s, "rtype") != "button" {
				continue
			}
			buttonID := ButtonID(jget(s, "rid").(string))
			buttonIndexes[buttonID] = buttonIndex
			buttonIndex++
		}
	}

	// Extract the scenes.
	for _, d := range data {
		if jget(d, "type") != "scene" {
			continue
		}
		sceneName, roomID, sceneID := SceneName(jget(d, "metadata", "name").(string)), RoomID(jget(d, "group", "rid").(string)), SceneID(jget(d, "id").(string))
		sceneNames[sceneID] = sceneName
		level := slices.Index(levelNames[:], sceneName)
		if level != -1 {
			scenes := roomScenes[roomID]
			scenes[level] = sceneID
			roomScenes[roomID] = scenes
		}
	}

	// Verify that each room has all 3 scenes set.
	for roomID, roomName := range roomNames {
		for i, s := range roomScenes[roomID] {
			if s == "" {
				return fmt.Errorf("huepush.MissingScene room=%s scene=%s", roomName, levelNames[i])
			}
		}
	}

	// Extract the action mappings.
	for _, d := range data {
		if jget(d, "type") != "behavior_instance" {
			continue
		}
		switchID := SwitchID(jget(d, "configuration", "device", "rid").(string))
		sw := switches[switchID]
		sw.BehaviorID = jget(d, "id").(string)
		buttonmap := [4]string{}
		for buttonID, button := range jget(d, "configuration", "buttons").(map[string]any) {
			buttonID := ButtonID(buttonID)
			roomID := RoomID(jget(button, "where", "0", "group", "rid").(string))
			buttonIndex := buttonIndexes[buttonID]
			bd := &buttondesc{ButtonID: buttonID, RoomID: roomID}
			sw.Buttons[buttonIndex] = bd
			buttonmap[buttonIndex] = fmt.Sprintf("%s %d %s\n", sw.SwitchName, buttonIndex, roomNames[roomID])

			// Extract the scene cycle.
			slots, ok := jget(button, "on_short_release", "scene_cycle_extended", "slots").([]any)
			if !ok || len(slots) != len(levelNames) {
				continue
			}
			for i, slot := range slots {
				if rid, ok := jget(slot, "0", "action", "recall", "rid").(string); ok {
					bd.Scenes[i] = SceneID(rid)
				}
			}
		}
	}

	if *flagDump {
		fmt.Printf("[RoomName] [GroupID] [LowSceneID] [MidSceneID] [HighSceneID]\n")
		for _, roomName := range slices.Sorted(maps.Keys(roomIDs)) {
			roomID := roomIDs[roomName]
			groupID := "?"
			// Find the grouped_light ID for this room.
			for _, d := range data {
				if jget(d, "type") != "grouped_light" {
					continue
				}
				if RoomID(jget(d, "owner", "rid").(string)) == roomID {
					groupID = jget(d, "id").(string)
					break
				}
			}
			fmt.Print(roomName, " ", groupID)
			for _, sceneID := range roomScenes[roomID] {
				fmt.Print(" ", sceneID)
			}
			fmt.Println()
		}
		return nil
	}

	// Load and check the intent.
	lineno := 0
	for line := range bytes.Lines(cfgBytes) {
		lineno++
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if bytes.HasPrefix(line, []byte("hue")) {
			continue
		}
		switchName, roomName, buttonIndex := SwitchName(""), RoomName(""), ButtonIndex(0)
		if _, err := fmt.Fscanf(bytes.NewReader(line), "%s %d %s", &switchName, &buttonIndex, &roomName); err != nil {
			return fmt.Errorf("huepush.ParseLine src=%s:%d (must be `SwitchName ButtonIndex RoomName`): %v", cfgfile, lineno, err)
		}
		switchID, roomID := switchIDs[switchName], roomIDs[roomName]
		if switchID == "" {
			return fmt.Errorf("huepush.SwitchNotFound switch=%s src=%s:%d", switchName, cfgfile, lineno)
		}
		if roomID == "" {
			return fmt.Errorf("huepush.RoomNotFound room=%s src=%s:%d", roomName, cfgfile, lineno)
		}
		sw := switches[switchID]
		if buttonIndex < 0 || int(buttonIndex) >= len(sw.Buttons) {
			return fmt.Errorf("huepush.InvalidButtonIndex got=%d src=%s:%d", buttonIndex, cfgfile, lineno)
		}
		bd := sw.Buttons[buttonIndex]

		wantButton := buttondesc{
			ButtonID: bd.ButtonID,
			RoomID:   roomID,
			Scenes:   roomScenes[roomID],
		}
		if gotRoomID := bd.RoomID; gotRoomID != roomID {
			fmt.Printf("huepush.ButtonNotAtIntent button=%s.%d got=%s want=%s\n", switchName, buttonIndex, roomNames[gotRoomID], roomName)
			*bd = wantButton
			sw.changed = true
			continue
		}
		if bd.Scenes != roomScenes[bd.RoomID] {
			gotSceneNames := [3]SceneName{}
			for i, id := range bd.Scenes {
				gotSceneNames[i] = sceneNames[id]
			}
			fmt.Printf("huepush.ButtonScenesNotAtIntent button=%s.%d got=%q want=%v gotSceneIDs=%q\n", switchName, buttonIndex, gotSceneNames, levelNames, bd.Scenes)
			sw.changed = true
		}
		if sw.changed {
			*bd = wantButton
		}
	}

	// Find switches to update.
	toupdate, toupdateNames := []*switchdesc{}, []string{}
	for _, switchName := range slices.Sorted(maps.Keys(switchIDs)) {
		if sw := switches[switchIDs[switchName]]; sw.changed {
			toupdate, toupdateNames = append(toupdate, sw), append(toupdateNames, string(switchName))
		}
	}
	if len(toupdateNames) == 0 {
		fmt.Printf("huepush.AlreadyAtIntent (the bridge is at intent, exiting)\n")
		return nil
	} else {
		fmt.Printf("huepush.SwitchesToUpdate switches=%s\n", strings.Join(toupdateNames, ","))
	}

	// Generate the update JSON for each switch and send it to the bridge.
	if !*flagApply {
		fmt.Printf("huepush.DryrunExit (use the -apply flag to apply the changes)\n")
		return nil
	}
	tmpl := template.Must(template.New("switchTemplate").Parse(switchTemplate))
	for _, switchName := range toupdateNames {
		fmt.Printf("huepush.UpdatingSwitch switch=%s\n", switchName)
		switchID := switchIDs[SwitchName(switchName)]
		sw := switches[switchID]
		w := &bytes.Buffer{}
		if err := tmpl.Execute(w, sw); err != nil {
			return fmt.Errorf("huepush.ExecuteTemplate switch=%s: %v", switchName, err)
		}

		updateRequest, err := http.NewRequestWithContext(ctx, "PUT", hueAddress+"/clip/v2/resource/behavior_instance/"+sw.BehaviorID, w)
		if err != nil {
			return fmt.Errorf("huepush.NewUpdateRequest switch=%s: %v", switchName, err)
		}
		updateRequest.Header.Set("Hue-Application-Key", hueKey)
		updateResponse, err := client.Do(updateRequest)
		if err != nil {
			return fmt.Errorf("huepush.SendUpdateRequest switch=%s: %v", switchName, err)
		}
		updateBody, err := io.ReadAll(updateResponse.Body)
		if err != nil {
			updateResponse.Body.Close()
			return fmt.Errorf("huepush.ReadUpdateResponseBody switch=%s: %v", switchName, err)
		}
		updateResponse.Body.Close()
		if updateResponse.StatusCode != 200 {
			return fmt.Errorf("huepush.Update switch=%s status=%q body:\n%s", switchName, updateResponse.Status, updateBody)
		}
	}

	return nil
}

type buttondesc struct {
	ButtonID
	RoomID
	Scenes [3]SceneID
}

type switchdesc struct {
	BehaviorID string
	SwitchID
	SwitchName
	Buttons [4]*buttondesc

	changed bool
}

const switchTemplate = `{
	"enabled": true,
	"metadata": {"name": "{{.SwitchName}}"},
	"configuration": {
		"model_id": "RWL022",
		"device": {"rid": "{{.SwitchID}}", "rtype": "device"},
		"buttons": {
			{{range $i, $b := .Buttons}}
			"{{$b.ButtonID}}": {
				"where": [{"group": {"rtype": "room", "rid": "{{$b.RoomID}}"}}],
				"on_long_press": {"action": "do_nothing"},
				"on_short_release": {
					"scene_cycle_extended": {
						"repeat_timeout": {"seconds": 2},
						"with_off": {"enabled": true},
						"slots": [
							{{range $j, $s := $b.Scenes}}
							[{
								"action": {
									"recall": {"rtype": "scene", "rid": "{{$s}}"}
								}
							}]{{if lt $j 2}},{{end}}
							{{end}}
						]
					}
				}
			}{{if lt $i 3}},{{end}}
			{{end}}
		}
	}
}
`
