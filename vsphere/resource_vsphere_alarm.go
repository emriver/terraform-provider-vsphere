// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: MPL-2.0

package vsphere

import (
	"context"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/vmware/govmomi/alarm"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/types"
	helper "github.com/vmware/terraform-provider-vsphere/vsphere/internal/helper/alarm"
	"github.com/vmware/terraform-provider-vsphere/vsphere/internal/helper/customattribute"
)

func resourceVSphereAlarm() *schema.Resource {
	return &schema.Resource{
		Create: resourceVSphereAlarmCreate,
		Read:   resourceVSphereAlarmRead,
		Update: resourceVSphereAlarmUpdate,
		Delete: resourceVSphereAlarmDelete,
		Importer: &schema.ResourceImporter{
			State: resourceVSphereAlarmImport,
		},
		SchemaVersion: 1,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Description:  "The name of the Alarm.",
				Required:     true,
				ValidateFunc: validation.NoZeroValues,
			},
			"description": {
				Type:         schema.TypeString,
				Description:  "The description of the alarm.",
				Required:     true,
				ValidateFunc: validation.NoZeroValues,
			},
			"enabled": {
				Type:        schema.TypeBool,
				Description: "Whether or not the alarm is enabled.",
				Default:     true,
				Optional:    true,
			},
			"entity_id": {
				Type:         schema.TypeString,
				Description:  "The id of the entity the alarm is attached to.",
				ValidateFunc: validation.NoZeroValues,
				Default:      "group-d1", // vsphere top folder
				Optional:     true,
				ForceNew:     true,
			},
			"entity_type": {
				Type:         schema.TypeString,
				Description:  "The id of the entity the alarm is attached to.",
				ValidateFunc: validation.NoZeroValues,
				Default:      "Folder", // vsphere top folder
				StateFunc:    func(i any) string { return helper.UcFirst(i.(string)) },
				Optional:     true,
				ForceNew:     true,
			},
			"expression_operator": {
				Type:        schema.TypeString,
				Description: "The link between alarm expressions.",
				Optional:    true,
				Default:     "or",
				ValidateFunc: validation.StringInSlice(
					[]string{"or", "and"},
					false,
				),
			},
			"expression": {
				Type:        schema.TypeList,
				Required:    true,
				Description: "The expressions defined in the alarm.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"type": {
							Type:     schema.TypeString,
							Default:  "event",
							Optional: true,
							ValidateFunc: validation.StringInSlice(
								[]string{
									string(helper.VSphereAlarmExpressionTypeEvent),
								},
								false,
							),
						},
						"event_type": {
							Type:      schema.TypeString,
							Optional:  true,
							Default:   "Event",
							StateFunc: func(i any) string { return helper.UcFirst(i.(string)) },
						},
						"event_type_id": {
							Type:     schema.TypeString,
							Required: true,
						},
						"object_type": {
							Type:      schema.TypeString,
							Required:  true,
							StateFunc: func(i any) string { return helper.UcFirst(i.(string)) },
							ValidateFunc: validation.StringInSlice(
								[]string{
									string(helper.VSphereAlarmObjectTypeHost),
								},
								false,
							),
						},
						"status": {
							Type:     schema.TypeString,
							Required: true,
							ValidateFunc: validation.StringInSlice(
								[]string{
									string(helper.VSphereAlarmStatusTypeRed),
									string(helper.VSphereAlarmStatusTypeYellow),
									string(helper.VSphereAlarmStatusTypeGreen),
								},
								false,
							),
						},
					},
				},
			},
			"action": {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "The actions defined in the alarm.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"type": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice([]string{"email, snmp, advanced action"}, false),
						},
						"start_state": {
							Type:     schema.TypeString,
							Required: true,
							ValidateFunc: validation.StringInSlice(
								[]string{
									string(helper.VSphereAlarmStatusTypeRed),
									string(helper.VSphereAlarmStatusTypeYellow),
									string(helper.VSphereAlarmStatusTypeGreen),
								},
								false,
							),
						},
						"final_state": {
							Type:     schema.TypeString,
							Required: true,
							ValidateFunc: validation.StringInSlice(
								[]string{
									string(helper.VSphereAlarmStatusTypeRed),
									string(helper.VSphereAlarmStatusTypeYellow),
									string(helper.VSphereAlarmStatusTypeGreen),
								},
								false,
							),
						},
						"repeat": {
							Type:     schema.TypeBool,
							Default:  false,
							Optional: true,
						},
						// for mails
						"to": {
							Type: schema.TypeString,
							//ConflictsWith: []string{"name", "args"},
							Optional: true,
						},
						"cc": {
							Type: schema.TypeString,
							//ConflictsWith: []string{"name", "args"},
							Optional: true,
						},
						"subject": {
							Type: schema.TypeString,
							//ConflictsWith: []string{"name", "args"},
							Optional: true,
						},
						"body": {
							Type: schema.TypeString,
							//ConflictsWith: []string{"name", "args"},
							Optional: true,
						},
						// for advanced actions
						"name": {
							Optional: true,
							Type:     schema.TypeString,
							//ConflictsWith: []string{"body", "cc", "to", "subject"},
							// ValidateFunc: validation.StringInSlice(
							// 	[]string{
							// 		string(helper.VsphereAlarmAdvancedActionTypeEnterMaintenance),
							// 	},
							// 	false,
							// ),
						},
						// TODO Manage different arg types (int32, bool, 0)?
						"args": {
							Type:     schema.TypeList,
							Optional: true,
							Elem:     schema.TypeString,
							//ConflictsWith: []string{"body", "cc", "to", "subject"},
						},
					},
				},
			},
			// Custom Attributes
			customattribute.ConfigKey: customattribute.ConfigSchema(),
		},
	}
}

func resourceVSphereAlarmCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient
	m, err := alarm.GetManager(client.Client)
	if err != nil {
		return err
	}

	entity, err := helper.FindEntity(client, d.Get("entity_type").(string), d.Get("entity_id").(string))
	if err != nil {
		return fmt.Errorf("alarm entity error: %s", err)
	}

	alarmSpec, err := helper.GetAlarmSpec(d)
	if err != nil {
		return fmt.Errorf("failed to generate alarm spec")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer cancel()
	ref, err := m.CreateAlarm(ctx, entity, alarmSpec)
	if err != nil {
		return fmt.Errorf("failed to create new alarm: %s", err)
	}
	d.SetId(ref.Reference().Value)

	// // wait for vsphere task to finish
	// tctx, tcancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	// defer tcancel()
	// if err := resp.WaitEx(tctx); err != nil {
	// 	return fmt.Errorf("error on waiting for alarm deletion task completion: %s", err)
	// }

	return resourceVSphereAlarmRead(d, meta)
}

func resourceVSphereAlarmRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient

	entity, err := helper.FindEntity(client, d.Get("entity_type").(string), d.Get("entity_id").(string))
	if err != nil {
		return fmt.Errorf("alarm entity error: %s", err)
	}

	al, err := helper.FromID(client, d.Id(), entity)
	if err != nil {
		return fmt.Errorf("cannot locate alarm: %s", err)
	}

	_ = d.Set("name", al.Info.Name)
	_ = d.Set("description", al.Info.Description)
	_ = d.Set("entity_type", al.Info.Entity.Type)
	_ = d.Set("entity_id", al.Info.Entity.Value)

	// Manage alarm expressions
	expressions := []map[string]string{}
	switch exp := al.Info.Expression.(type) {
	case *types.OrAlarmExpression:
		_ = d.Set("expressions_logic", "or")
		e, err := helper.GetExpressions(exp.Expression)
		if err != nil {
			return err
		}
		expressions = append(expressions, e...)
	case *types.AndAlarmExpression:
		_ = d.Set("expressions_logic", "and")
		e, err := helper.GetExpressions(exp.Expression)
		if err != nil {
			return err
		}
		expressions = append(expressions, e...)
	}
	_ = d.Set("expressions", expressions)

	// Manage actions
	if al.Info.Action != nil {
		switch alarmAction := al.Info.Action.(type) {
		case *types.GroupAlarmAction:
			actions, err := helper.GetAlarmActions(alarmAction.Action)
			if err != nil {
				return err
			}
			_ = d.Set("actions", actions)
		case *types.AlarmAction:
			action, err := helper.GetAlarmActions([]types.BaseAlarmAction{alarmAction})
			if err != nil {
				return err
			}
			_ = d.Set("actions", action)
		default:
			return fmt.Errorf("unmanaged alarm action type: %s", reflect.TypeOf(alarmAction))
		}
	}
	return nil
}

func resourceVSphereAlarmUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient

	entity, err := helper.FindEntity(client, d.Get("entity_type").(string), d.Get("entity_id").(string))
	if err != nil {
		return fmt.Errorf("alarm entity error: %s", err)
	}

	al, err := helper.FromID(client, d.Id(), entity)
	if err != nil {
		return fmt.Errorf("cannot locate alarm: %s", err)
	}

	// if the entity changed, we need to delete the old and create the new entity
	if d.HasChange("entity_id") || d.HasChange("entity_type") {
		err = resourceVSphereAlarmDelete(d, meta)
		if err != nil {
			return fmt.Errorf("failed to delete alarm: %s", err)
		}
		return resourceVSphereAlarmCreate(d, meta)
	}

	alarmSpec, err := helper.GetAlarmSpec(d)
	if err != nil {
		return fmt.Errorf("failed to generate alarm spec")
	}

	//		oldp, newp := d.GetChange("path")
	tctx, tcancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer tcancel()
	_, err = methods.ReconfigureAlarm(tctx, client.RoundTripper, &types.ReconfigureAlarm{
		This: al.Self,
		Spec: alarmSpec,
	})
	if err != nil {
		return fmt.Errorf("failed to reconfigure alarm: %s", err)
	}
	return resourceVSphereAlarmRead(d, meta)
}

func resourceVSphereAlarmDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*Client).vimClient

	var ref types.ManagedObjectReference

	if !ref.FromString(d.Id()) {
		ref.Type = "Alarm"
		ref.Value = d.Id()
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	defer cancel()
	_, err := methods.RemoveAlarm(ctx, client.Client, &types.RemoveAlarm{
		This: ref,
	})
	if err != nil {
		return fmt.Errorf("cannot delete alarm: %s", err)
	}

	// // wait for vsphere task to finish
	// tctx, tcancel := context.WithTimeout(context.Background(), defaultAPITimeout)
	// defer tcancel()
	// if err := resp.WaitEx(tctx); err != nil {
	// 	return fmt.Errorf("error on waiting for alarm deletion task completion: %s", err)
	// }

	return nil
}

func resourceVSphereAlarmImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	return []*schema.ResourceData{d}, nil
}
