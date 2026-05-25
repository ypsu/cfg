// The tool huepush updates my Hue 4 button switches to the intent described in $HOME/.config/hue.cfg.
//
// If a switch doesn't seem to be working after a push, then reconfigure it in the official Hue app.
// If you see "Not configured in this app" in the app, then it means it's misconfigured.
// Alternatively remove the switch and readd it.
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
type TargetID string
type TargetName string
type SceneID string
type SceneName string
type SwitchID string
type SwitchName string
type TargetKind string // either "room" or "zone"

func jget(v any, path string) any {
	for path != "" && v != nil {
		if v == nil {
			return nil
		}
		head, tail, _ := strings.Cut(path, ".")
		if idx, err := strconv.Atoi(head); err == nil { // on success
			v, path = v.([]any)[idx], tail
		} else {
			v, path = v.(map[string]any)[head], tail
		}
	}
	return v
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
	flagDump := flag.Bool("dump", false, "Dump the parsed configuration.")
	flagRawdump := flag.Bool("rawdump", false, "Dump the raw configuration json.")
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
	if *flagRawdump {
		w := &bytes.Buffer{}
		if err := json.Indent(w, configBody, "", "  "); err != nil {
			return fmt.Errorf("huepush.IndentRawConfig: %v", err)
		}
		os.Stdout.Write(w.Bytes())
		return nil
	}
	var configJSON any
	if err := json.Unmarshal(configBody, &configJSON); err != nil {
		return fmt.Errorf("huepush.UnmarshalConfig: %v", err)
	}
	data := jget(configJSON, "data").([]any)

	// Set up the data structures.
	switches, switchIDs := map[SwitchID]*switchdesc{}, map[SwitchName]SwitchID{}
	targetNames, targetIDs, targetKinds := map[TargetID]TargetName{}, map[TargetName]TargetID{}, map[TargetID]TargetKind{}
	sceneNames, sceneIDs := map[SceneID]SceneName{}, map[SceneName]SceneID{}

	// Find the scriptID.
	scriptID := ""
	for _, d := range data {
		if jget(d, "type").(string) == "behavior_script" && jget(d, "description").(string) == "Generic switches script" {
			scriptID = jget(d, "id").(string)
			break
		}
	}
	if scriptID == "" {
		return fmt.Errorf("huepush.BehaviorScriptNotFound")
	}

	// Extract the targets.
	for _, d := range data {
		kind := jget(d, "type").(string)
		if kind != "room" && kind != "zone" {
			continue
		}
		name, id := TargetName(jget(d, "metadata.name").(string)), TargetID(jget(d, "id").(string))
		targetNames[id], targetIDs[name], targetKinds[id] = name, id, TargetKind(kind)
	}

	// Extract the switches.
	for _, d := range data {
		if jget(d, "type").(string) != "device" || jget(d, "product_data.model_id") != "RWL022" {
			continue
		}
		switchName, switchID := SwitchName(jget(d, "metadata.name").(string)), SwitchID(jget(d, "id").(string))
		switchIDs[switchName], switches[switchID] = switchID, &switchdesc{SwitchID: switchID, SwitchName: switchName, ScriptID: scriptID, Buttons: map[ButtonIndex]*buttondesc{}}
	}

	// Extract the scenes.
	for _, d := range data {
		if jget(d, "type") != "scene" {
			continue
		}
		localName, targetID, sceneID := SceneName(jget(d, "metadata.name").(string)), TargetID(jget(d, "group.rid").(string)), SceneID(jget(d, "id").(string))
		name := SceneName(targetNames[targetID]) + "." + localName
		sceneIDs[name], sceneNames[sceneID] = sceneID, name
	}

	// Extract the buttons.
	for _, d := range data {
		if jget(d, "type") != "button" {
			continue
		}
		buttonID, ownerID, buttonIndex := ButtonID(jget(d, "id").(string)), SwitchID(jget(d, "owner.rid").(string)), ButtonIndex(jget(d, "metadata.control_id").(float64))
		switches[ownerID].Buttons[buttonIndex] = &buttondesc{ButtonID: buttonID}
	}

	// Extract the action mappings.
	for _, d := range data {
		if jget(d, "type") != "behavior_instance" {
			continue
		}
		switchID := SwitchID(jget(d, "configuration.device.rid").(string))
		sw := switches[switchID]
		sw.BehaviorID = jget(d, "id").(string)
		for buttonID, button := range jget(d, "configuration.buttons").(map[string]any) {
			buttonID := ButtonID(buttonID)
			targetID := TargetID(jget(button, "where.0.group.rid").(string))
			var bd *buttondesc
			for _, button := range sw.Buttons {
				if button.ButtonID == buttonID {
					bd = button
					break
				}
			}
			bd.TargetID = targetID
			bd.TargetKind = targetKinds[targetID]

			if action, ok := jget(button, "on_short_release.action").(string); ok && action == "all_off" {
				bd.WithOff = true
				continue
			}

			if rid, ok := jget(button, "on_short_release.recall_single_extended.actions.0.action.recall.rid").(string); ok {
				bd.Scenes = append(bd.Scenes, SceneID(rid))
				continue
			}

			// Extract the scene cycle.
			slots, ok := jget(button, "on_short_release.scene_cycle_extended.slots").([]any)
			if !ok {
				continue
			}
			if enabled, ok := jget(button, "on_short_release.scene_cycle_extended.with_off.enabled").(bool); ok {
				bd.WithOff = enabled
			}
			for _, slot := range slots {
				if rid, ok := jget(slot, "0.action.recall.rid").(string); ok {
					bd.Scenes = append(bd.Scenes, SceneID(rid))
				}
			}
		}
	}

	if *flagDump {
		fmt.Printf("Available rooms:\n")
		for _, name := range slices.Sorted(maps.Keys(targetIDs)) {
			if targetKinds[targetIDs[name]] == "room" {
				fmt.Printf("  %-20s %s\n", name, targetIDs[name])
			}
		}
		fmt.Printf("Available zones:\n")
		for _, name := range slices.Sorted(maps.Keys(targetIDs)) {
			if targetKinds[targetIDs[name]] == "zone" {
				fmt.Printf("  %-20s %s\n", name, targetIDs[name])
			}
		}
		fmt.Printf("Available scenes:\n")
		for _, name := range slices.Sorted(maps.Keys(sceneIDs)) {
			fmt.Printf("  %-20s %s\n", name, sceneIDs[name])
		}
		fmt.Printf("Switch configuration:\n")
		for _, switchName := range slices.Sorted(maps.Keys(switchIDs)) {
			sw := switches[switchIDs[switchName]]
			for _, i := range slices.Sorted(maps.Keys(sw.Buttons)) {
				button := sw.Buttons[i]
				fmt.Printf("  %s %d", switchName, i)
				for _, sceneID := range button.Scenes {
					fmt.Printf(" %s", sceneNames[sceneID])
				}
				if button.WithOff {
					fmt.Printf(" %s.Off", targetNames[button.TargetID])
				}
				fmt.Printf("\n")
			}
		}
		return nil
	}

	// Load and check the intent.
	lineno := 0
	for line := range strings.Lines(string(cfgBytes)) {
		lineno++
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0][0] == '#' {
			continue
		}
		if strings.HasPrefix(fields[0], "hue") {
			continue
		}
		if len(fields) <= 2 {
			return fmt.Errorf("huepush.ParseLine src=%s:%d (must be SwitchName ButtonIndex Scenes..)", cfgfile, lineno)
		}
		switchName, buttonIndex := SwitchName(fields[0]), ButtonIndex(0)
		if v, err := strconv.Atoi(fields[1]); err != nil {
			return fmt.Errorf("huepush.ParseButtonSpec src=%s:%d: %v", cfgfile, lineno, err)
		} else {
			buttonIndex = ButtonIndex(v)
		}
		buttonTargetName, _, _ := strings.Cut(fields[2], ".")
		targetName := TargetName(buttonTargetName)
		switchID, targetID := switchIDs[switchName], targetIDs[targetName]
		if switchID == "" {
			return fmt.Errorf("huepush.SwitchNotFound switch=%s src=%s:%d", switchName, cfgfile, lineno)
		}
		if targetIDs[targetName] == "" {
			return fmt.Errorf("huepush.InvalidButtonTarget src=%s:%d target=%s", cfgfile, lineno, targetName)
		}
		sw := switches[switchID]
		bd := sw.Buttons[buttonIndex]
		if bd == nil {
			return fmt.Errorf("huepush.ButtonNotFound src=%s:%d button=%s.%d", cfgfile, lineno, switchName, buttonIndex)
		}

		wantButton := buttondesc{
			ButtonID:   bd.ButtonID,
			TargetID:   targetID,
			TargetKind: targetKinds[targetID],
		}
		for i := 2; i < len(fields); i++ {
			sceneName := fields[i]
			sceneTarget, _, _ := strings.Cut(sceneName, ".")
			if TargetName(sceneTarget) != targetName {
				return fmt.Errorf("huepush.InconsistentButtonTarget src=%s:%d got=%s want=%s", cfgfile, lineno, sceneTarget, targetName)
			}
			if i == len(fields)-1 && strings.HasSuffix(sceneName, ".Off") {
				wantButton.WithOff = true
			} else if sceneID, ok := sceneIDs[SceneName(sceneName)]; !ok {
				return fmt.Errorf("huepush.SceneNotFound src=%s:%d scene=%s", cfgfile, lineno, sceneName)
			} else {
				wantButton.Scenes = append(wantButton.Scenes, sceneID)
			}
		}
		var gotSceneNames []string
		for _, sceneID := range bd.Scenes {
			gotSceneNames = append(gotSceneNames, string(sceneNames[sceneID]))
		}
		if bd.WithOff {
			gotSceneNames = append(gotSceneNames, string(targetNames[bd.TargetID])+".Off")
		}
		if !slices.Equal(gotSceneNames, fields[2:]) {
			fmt.Printf("huepush.ButtonScenesNotAtIntent button=%s.%d got=%s want=%s\n", switchName, buttonIndex, strings.Join(gotSceneNames, ","), strings.Join(fields[2:], ","))
			*bd = wantButton
			sw.changed = true
		}
	}

	// Find switches to update.
	toupdateNames := []string{}
	for _, switchName := range slices.Sorted(maps.Keys(switchIDs)) {
		if sw := switches[switchIDs[switchName]]; sw.changed {
			toupdateNames = append(toupdateNames, string(switchName))
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
		buf := w.String()
		buf = strings.NewReplacer(" ", "", "\t", "", "\n", "").Replace(buf)
		buf = strings.NewReplacer(",}", "}", ",]", "]").Replace(buf)
		w.Reset()
		if err := json.Indent(w, []byte(buf), "", "  "); err != nil {
			return fmt.Errorf("huepush.InvalidUpdateJSON switch=%s: %v", switchName, err)
		}
		payload := w.Bytes()

		var updateRequest *http.Request
		method, urlPath := "POST", "/clip/v2/resource/behavior_instance"
		if sw.BehaviorID != "" {
			method, urlPath = "PUT", urlPath+"/"+sw.BehaviorID
		}
		updateRequest, err = http.NewRequestWithContext(ctx, method, hueAddress+urlPath, w)
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
		if updateResponse.StatusCode/100 != 2 {
			return fmt.Errorf("huepush.Update switch=%s status=%q method=%s url=%s payload: %s\nresponse:\n%s", switchName, updateResponse.Status, method, urlPath, payload, updateBody)
		}
	}
	return nil
}

type buttondesc struct {
	ButtonID
	TargetKind
	TargetID
	Scenes  []SceneID
	WithOff bool
}

type switchdesc struct {
	SwitchID
	SwitchName
	BehaviorID string
	ScriptID   string
	Buttons    map[ButtonIndex]*buttondesc

	changed bool
}

const switchTemplate = `{
	"enabled": true,
	"metadata": {"name": "{{.SwitchName}}"},
	{{if eq .BehaviorID ""}}"script_id": "{{.ScriptID}}",{{end}}
	"configuration": {
		"model_id": "RWL022",
		"device": {"rid": "{{.SwitchID}}", "rtype": "device"},
		"buttons": {
			{{range $i, $b := .Buttons}}
			"{{$b.ButtonID}}": {
				"where": [{"group": {"rtype": "{{$b.TargetKind}}", "rid": "{{$b.TargetID}}"}}],
				"on_long_press": {"action": "do_nothing"},
				"on_short_release": {
					{{if $b.Scenes}}
						"scene_cycle_extended": {
							"repeat_timeout": {"seconds": 2},
							"with_off": {"enabled": {{$b.WithOff}}},
							"slots": [
								{{range $j, $s := $b.Scenes}}
								[{
									"action": {
										"recall": {"rtype": "scene", "rid": "{{$s}}"}
									}
								}],
								{{end}}
							]
						}
					{{else}}
						"action": "all_off",
					{{end}}
				}
			},
			{{end}}
		}
	}
}
`
