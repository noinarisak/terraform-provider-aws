// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package networkmonitor

import (
	"context"
	"fmt"
	"time"

	"github.com/YakDriver/regexache"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkmonitor"
	awstypes "github.com/aws/aws-sdk-go-v2/service/networkmonitor/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/id"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-provider-aws/internal/enum"
	"github.com/hashicorp/terraform-provider-aws/internal/errs"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/fwdiag"
	"github.com/hashicorp/terraform-provider-aws/internal/framework"
	fwflex "github.com/hashicorp/terraform-provider-aws/internal/framework/flex"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	"github.com/hashicorp/terraform-provider-aws/names"
)

// @FrameworkResource("aws_networkmonitor_monitor", name="Monitor")
// @Tags(identifierAttribute="arn")
func newMonitorResource(context.Context) (resource.ResourceWithConfigure, error) {
	return &monitorResource{}, nil
}

type monitorResource struct {
	framework.ResourceWithModel[monitorResourceModel]
	framework.WithImportByID
}

func (r *monitorResource) Schema(ctx context.Context, request resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"aggregation_period": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Validators: []validator.Int64{
					int64validator.OneOf(30, 60),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			names.AttrARN: framework.ARNAttributeComputedOnly(),
			names.AttrID:  framework.IDAttribute(),
			"monitor_name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexache.MustCompile("[a-zA-Z0-9_-]+"), "Must match [a-zA-Z0-9_-]+"),
					stringvalidator.LengthBetween(1, 255),
				},
			},
			names.AttrTags:    tftags.TagsAttribute(),
			names.AttrTagsAll: tftags.TagsAttributeComputedOnly(),
		},
	}
}

func (r *monitorResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var data monitorResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	conn := r.Meta().NetworkMonitorClient(ctx)

	name := data.MonitorName.ValueString()
	input := &networkmonitor.CreateMonitorInput{}
	response.Diagnostics.Append(fwflex.Expand(ctx, data, input)...)
	if response.Diagnostics.HasError() {
		return
	}

	input.ClientToken = aws.String(id.UniqueId())
	input.Tags = getTagsIn(ctx)

	_, err := conn.CreateMonitor(ctx, input)

	if err != nil {
		response.Diagnostics.AddError(fmt.Sprintf("creating CloudWatch Network Monitor Monitor (%s)", name), err.Error())

		return
	}

	output, err := waitMonitorReady(ctx, conn, data.MonitorName.ValueString())
	if err != nil {
		response.Diagnostics.AddError(fmt.Sprintf("waiting for CloudWatch Network Monitor Monitor (%s) create", data.MonitorName.ValueString()), err.Error())

		return
	}

	response.Diagnostics.Append(fwflex.Flatten(ctx, output, &data)...)
	if response.Diagnostics.HasError() {
		return
	}
	data.setID()

	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

func (r *monitorResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var data monitorResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	if err := data.InitFromID(); err != nil {
		response.Diagnostics.AddError("parsing resource ID", err.Error())

		return
	}

	conn := r.Meta().NetworkMonitorClient(ctx)

	output, err := findMonitorByName(ctx, conn, data.MonitorName.ValueString())

	if tfresource.NotFound(err) {
		response.Diagnostics.Append(fwdiag.NewResourceNotFoundWarningDiagnostic(err))
		response.State.RemoveResource(ctx)

		return
	}

	if err != nil {
		response.Diagnostics.AddError(fmt.Sprintf("reading CloudWatch Network Monitor Monitor (%s)", data.MonitorName.ValueString()), err.Error())

		return
	}

	response.Diagnostics.Append(fwflex.Flatten(ctx, output, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	setTagsOut(ctx, output.Tags)

	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

func (r *monitorResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var state, plan monitorResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	conn := r.Meta().NetworkMonitorClient(ctx)

	if !plan.AggregationPeriod.Equal(state.AggregationPeriod) {
		input := &networkmonitor.UpdateMonitorInput{}
		response.Diagnostics.Append(fwflex.Expand(ctx, plan, input)...)
		if response.Diagnostics.HasError() {
			return
		}

		_, err := conn.UpdateMonitor(ctx, input)

		if err != nil {
			response.Diagnostics.AddError(fmt.Sprintf("updating CloudWatch Network Monitor Monitor (%s)", plan.ID.ValueString()), err.Error())

			return
		}

		output, err := waitMonitorReady(ctx, conn, plan.ID.ValueString())
		if err != nil {
			response.Diagnostics.AddError(fmt.Sprintf("waiting for CloudWatch Network Monitor Monitor (%s) update", plan.ID.ValueString()), err.Error())

			return
		}
		response.Diagnostics.Append(fwflex.Flatten(ctx, output, &plan)...)
		if response.Diagnostics.HasError() {
			return
		}
	}

	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (r *monitorResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var data monitorResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	conn := r.Meta().NetworkMonitorClient(ctx)

	_, err := conn.DeleteMonitor(ctx, &networkmonitor.DeleteMonitorInput{
		MonitorName: fwflex.StringFromFramework(ctx, data.MonitorName),
	})

	if errs.IsA[*awstypes.ResourceNotFoundException](err) {
		return
	}

	if err != nil {
		response.Diagnostics.AddError(fmt.Sprintf("deleting CloudWatch Network Monitor Monitor (%s)", data.MonitorName.ValueString()), err.Error())

		return
	}

	if _, err := waitMonitorDeleted(ctx, conn, data.MonitorName.ValueString()); err != nil {
		response.Diagnostics.AddError(fmt.Sprintf("waiting for CloudWatch Network Monitor Monitor (%s) delete", data.MonitorName.ValueString()), err.Error())

		return
	}
}

func findMonitorByName(ctx context.Context, conn *networkmonitor.Client, name string) (*networkmonitor.GetMonitorOutput, error) {
	input := &networkmonitor.GetMonitorInput{
		MonitorName: aws.String(name),
	}

	output, err := conn.GetMonitor(ctx, input)

	if errs.IsA[*awstypes.ResourceNotFoundException](err) {
		return nil, &retry.NotFoundError{
			LastError:   err,
			LastRequest: input,
		}
	}

	if err != nil {
		return nil, err
	}

	if output == nil {
		return nil, tfresource.NewEmptyResultError(input)
	}

	return output, nil
}

func statusMonitor(ctx context.Context, conn *networkmonitor.Client, name string) retry.StateRefreshFunc {
	return func() (any, string, error) {
		output, err := findMonitorByName(ctx, conn, name)

		if tfresource.NotFound(err) {
			return nil, "", nil
		}

		if err != nil {
			return nil, "", err
		}

		return output, string(output.State), nil
	}
}

func waitMonitorReady(ctx context.Context, conn *networkmonitor.Client, name string) (*networkmonitor.GetMonitorOutput, error) {
	const (
		timeout = time.Minute * 10
	)
	stateConf := &retry.StateChangeConf{
		Pending:    enum.Slice(awstypes.MonitorStatePending),
		Target:     enum.Slice(awstypes.MonitorStateActive, awstypes.MonitorStateInactive),
		Refresh:    statusMonitor(ctx, conn, name),
		Timeout:    timeout,
		MinTimeout: 10 * time.Second,
	}

	outputRaw, err := stateConf.WaitForStateContext(ctx)

	if output, ok := outputRaw.(*networkmonitor.GetMonitorOutput); ok {
		return output, err
	}

	return nil, err
}

func waitMonitorDeleted(ctx context.Context, conn *networkmonitor.Client, name string) (*networkmonitor.GetMonitorOutput, error) {
	const (
		timeout = time.Minute * 10
	)
	stateConf := &retry.StateChangeConf{
		Pending:    enum.Slice(awstypes.MonitorStateDeleting, awstypes.MonitorStateActive, awstypes.MonitorStateInactive),
		Target:     []string{},
		Refresh:    statusMonitor(ctx, conn, name),
		Timeout:    timeout,
		MinTimeout: 10 * time.Second,
	}

	outputRaw, err := stateConf.WaitForStateContext(ctx)

	if output, ok := outputRaw.(*networkmonitor.GetMonitorOutput); ok {
		return output, err
	}

	return nil, err
}

type monitorResourceModel struct {
	framework.WithRegionModel
	AggregationPeriod types.Int64  `tfsdk:"aggregation_period"`
	ID                types.String `tfsdk:"id"`
	MonitorARN        types.String `tfsdk:"arn"`
	MonitorName       types.String `tfsdk:"monitor_name"`
	Tags              tftags.Map   `tfsdk:"tags"`
	TagsAll           tftags.Map   `tfsdk:"tags_all"`
}

func (model *monitorResourceModel) InitFromID() error {
	model.MonitorName = model.ID

	return nil
}

func (model *monitorResourceModel) setID() {
	model.ID = model.MonitorName
}
