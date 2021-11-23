package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os/exec"
)

func findwsdata(t map[string]interface{}) (focused int, allwindows []int) {
	tp := t["type"].(string)
	id := 0
	if w, ok := t["window"].(float64); ok {
		id = int(w)
	}
	if t["focused"].(bool) {
		return id, []int{id}
	}
	if id > 0 {
		return -1, []int{id}
	}
	ns := []map[string]interface{}{}
	for _, n := range t["nodes"].([]interface{}) {
		ns = append(ns, n.(map[string]interface{}))
	}
	for _, n := range t["floating_nodes"].([]interface{}) {
		ns = append(ns, n.(map[string]interface{}))
	}

	f, ws := -1, []int{}
	for _, n := range ns {
		af, aws := findwsdata(n)
		if tp != "output" || af > 0 {
			ws = append(ws, aws...)
			if af > 0 {
				f = af
			}
		}
	}
	if tp == "workspace" && f == -1 {
		return -1, nil
	}
	return f, ws
}

func main() {
	prevFlag := flag.Bool("p", false, "focus the previous window.")
	flag.Parse()

	i3tree := &bytes.Buffer{}
	cmd := exec.Command("i3-msg", "-tget_tree")
	cmd.Stdout = i3tree
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}

	root := map[string]interface{}{}
	json.Unmarshal(i3tree.Bytes(), &root)
	focused, ws := findwsdata(root)
	if len(ws) <= 1 {
		return
	}

	offset := 1
	if *prevFlag {
		offset = len(ws) - 1
	}
	for i, w := range ws {
		if w == focused {
			ni := (i + offset) % len(ws)
			cmd := exec.Command("i3-msg", fmt.Sprintf("[id=%d]", ws[ni]), "focus")
			if err := cmd.Run(); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	log.Fatal("focused window not found")
}
