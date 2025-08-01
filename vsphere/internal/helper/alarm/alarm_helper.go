// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: MPL-2.0

package alarm

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"strings"
	"unicode"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/alarm"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/terraform-provider-vsphere/vsphere/internal/helper/provider"
)

// VSphereAlarmExpressionType is an enumeration type for vSphere alarm expression types.
type VSphereAlarmExpressionType string

const (
	VSphereAlarmExpressionTypeEvent = VSphereAlarmExpressionType("event")
)

type VSphereAlarmObjectType string

const (
	VSphereAlarmObjectTypeHost = VSphereAlarmObjectType("HostSystem")
)

type VSphereAlarmStatusType string

const (
	VSphereAlarmStatusTypeRed    = VSphereAlarmStatusType("red")
	VSphereAlarmStatusTypeYellow = VSphereAlarmStatusType("yellow")
	VSphereAlarmStatusTypeGreen  = VSphereAlarmStatusType("green")
)

type VsphereAlarmAdvancedActionType string

const (
	VsphereAlarmAdvancedActionTypeEnterMaintenance = VsphereAlarmAdvancedActionType("enter_maintenance")
)

// PathIsEmpty checks a folder path to see if it's "empty" (ie: would resolve
// to the root inventory path for a given type in a datacenter - "" or "/").
func PathIsEmpty(path string) bool {
	return path == "" || path == "/"
}

// NormalizePath is a SchemaStateFunc that normalizes a folder path.
func NormalizePath(v interface{}) string {
	p := v.(string)
	if PathIsEmpty(p) {
		return ""
	}
	return strings.TrimPrefix(path.Clean(p), "/")
}

func FindEntity(client *govmomi.Client, objType string, id string) (object.Reference, error) {
	finder := find.NewFinder(client.Client, false)

	ref := types.ManagedObjectReference{
		Type:  objType,
		Value: id,
	}

	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	return finder.ObjectReference(ctx, ref)
}

// FromID locates a Folder by its managed object reference ID.
func FromID(client *govmomi.Client, id string, entity object.Reference) (*mo.Alarm, error) {
	m, err := alarm.GetManager(client.Client)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	alarms, err := m.GetAlarm(ctx, entity)
	if err != nil {
		return nil, err
	}
	for _, al := range alarms {
		if al.Reference().Value == id {
			return &al, nil
		}
	}

	return nil, fmt.Errorf("alarm %s not found", id)
}

func UcFirst(s string) string {
	r := []rune(s)
	return string(append([]rune{unicode.ToUpper(r[0])}, r[1:]...))
}

func GetExpressions(b []types.BaseAlarmExpression) ([]map[string]string, error) {
	expressions := []map[string]string{}
	for _, e := range b {
		switch subExp := e.(type) {
		case *types.EventAlarmExpression:
			expressions = append(expressions, map[string]string{
				"type":        "event",
				"eventType":   subExp.EventType,
				"eventTypeId": subExp.EventTypeId,
				"objectType":  subExp.ObjectType,
			})
		default:
			return expressions, fmt.Errorf("unknown expression type: %s", reflect.TypeOf(subExp))
		}
	}
	return expressions, nil
}

func GetAlarmActions(b []types.BaseAlarmAction) ([]map[string]types.AnyType, error) {
	actions := []map[string]types.AnyType{}
	for _, a := range b {
		switch action := a.(type) {
		case *types.AlarmTriggeringAction:
			switch at := action.Action.(type) {
			case *types.SendEmailAction:
				actions = append(actions, map[string]types.AnyType{
					"type":        "email",
					"to":          at.ToList,
					"cc":          at.CcList,
					"subject":     at.Subject,
					"body":        at.Body,
					"final_state": action.TransitionSpecs[0].FinalState,
					"start_state": action.TransitionSpecs[0].StartState,
					"repeat":      action.TransitionSpecs[0].Repeats,
				})
			case *types.MethodAction:
				args := []types.AnyType{}
				for _, a := range at.Argument {
					args = append(args, a.Value)
				}
				actions = append(actions, map[string]types.AnyType{
					"type":        "advanced",
					"name":        at.Name,
					"args":        args,
					"final_state": action.TransitionSpecs[0].FinalState,
					"start_state": action.TransitionSpecs[0].StartState,
					"repeat":      action.TransitionSpecs[0].Repeats,
				})
			}
		default:
			return actions, fmt.Errorf("unknown expression type: %s", reflect.TypeOf(a))
		}
	}
	return actions, nil
}

func GetStatusFromString(s string) (types.ManagedEntityStatus, error) {
	switch s {
	case "red":
		return types.ManagedEntityStatusRed, nil
	case "yellow":
		return types.ManagedEntityStatusYellow, nil
	case "green":
		return types.ManagedEntityStatusGreen, nil
	case "gray":
		return types.ManagedEntityStatusGray, nil
	}
	return types.ManagedEntityStatusGreen, fmt.Errorf("unknown status: %s", s)
}

func GetAlarmSpec(d *schema.ResourceData) (*types.AlarmSpec, error) {
	// Expressions
	fromExpressions, err := GetBaseExpressions(d)
	if err != nil {
		return nil, fmt.Errorf("failed to compute expressions: %s", err)
	}
	var expressions types.BaseAlarmExpression
	switch d.Get("expression_operator").(string) {
	case "or":
		expressions = &types.OrAlarmExpression{
			Expression: fromExpressions,
		}
	case "and":
		expressions = &types.AndAlarmExpression{
			Expression: fromExpressions,
		}
	}

	// Actions TODO

	return &types.AlarmSpec{
		Name:            d.Get("name").(string),
		Description:     d.Get("description").(string),
		Enabled:         d.Get("enabled").(bool),
		SystemName:      "",
		Expression:      expressions,
		Action:          nil,
		ActionFrequency: 0,
		Setting: &types.AlarmSetting{
			ToleranceRange:     0,
			ReportingFrequency: 300,
		},
	}, nil
}

func GetBaseExpressions(d *schema.ResourceData) ([]types.BaseAlarmExpression, error) {
	var fromExpressions []types.BaseAlarmExpression
	for i := range d.Get("expression").([]any) {
		status, err := GetStatusFromString(d.Get(fmt.Sprintf("expression.%d.status", i)).(string))
		if err != nil {
			return nil, fmt.Errorf("alarm expression error: %s", err)
		}
		switch d.Get(fmt.Sprintf("expression.%d.type", i)).(string) {
		case "event":
			fromExpressions = append(fromExpressions, &types.EventAlarmExpression{
				ObjectType:  d.Get(fmt.Sprintf("expression.%d.object_type", i)).(string),
				EventType:   d.Get(fmt.Sprintf("expression.%d.event_type", i)).(string),
				Status:      status,
				EventTypeId: d.Get(fmt.Sprintf("expression.%d.event_type_id", i)).(string),
			})
		}
	}
	return fromExpressions, nil
}
