// gosuflow implements the GO StrUct FLOW runner.
package gosuflow

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

func Run(ctx context.Context, workflow any) error {
	wftype := reflect.Indirect(reflect.ValueOf(workflow)).Type()
	if wftype.Kind() != reflect.Struct {
		return fmt.Errorf("gosuflow.InvalidWorkflow want=struct got=%s type=%s", wftype.Kind(), wftype.Name())
	}

	// Verify that the struct has matching sections and functions.
	var sections, methods []string
	for i := range wftype.NumField() {
		if name, ok := strings.CutSuffix(wftype.Field(i).Name, "Section"); ok && len(name) >= 1 && 'A' <= name[0] && name[0] <= 'Z' {
			sections = append(sections, name)
		}
	}
	wftype = reflect.ValueOf(workflow).Type()
	wfvalue := reflect.ValueOf(workflow)
	methodtype := reflect.TypeOf(func(context.Context) error { return nil })
	for i := range wftype.NumMethod() {
		if !wfvalue.Method(i).Type().AssignableTo(methodtype) {
			return fmt.Errorf("gosuflow.InvalidMethodType method=%s want: func(context.Context) error", wftype.Method(i).Name)
		}
		methods = append(methods, wftype.Method(i).Name)
	}
	for _, section := range sections {
		if !slices.Contains(methods, section) {
			return fmt.Errorf("gosuflow.MissingMethod method=%s", section)
		}
	}
	for _, method := range methods {
		if !slices.Contains(sections, method) {
			return fmt.Errorf("gosuflow.MissingSection section=%sSection", method)
		}
	}

	// Run the individual sections to the first error.
	args := []reflect.Value{reflect.ValueOf(workflow), reflect.ValueOf(ctx)}
	for _, section := range sections {
		method, _ := wftype.MethodByName(section)
		result := method.Func.Call(args)
		if err, ok := result[0].Interface().(error); ok && err != nil {
			return fmt.Errorf("gosuflow.RunStep step=%s: %v", section, err)
		}
	}
	return nil
}
