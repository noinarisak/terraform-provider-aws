// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ec2

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	awstypes "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/hashicorp/aws-sdk-go-base/v2/awsv1shim/v2/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/sdkdiag"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
)

// @SDKDataSource("aws_prefix_list")
func DataSourcePrefixList() *schema.Resource {
	return &schema.Resource{
		ReadWithoutTimeout: dataSourcePrefixListRead,

		Timeouts: &schema.ResourceTimeout{
			Read: schema.DefaultTimeout(20 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"cidr_blocks": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"filter": customFiltersSchema(),
			"name": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"prefix_list_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
		},
	}
}

func dataSourcePrefixListRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).EC2Client(ctx)

	input := &ec2.DescribePrefixListsInput{}

	if v, ok := d.GetOk("name"); ok {
		input.Filters = append(input.Filters, newAttributeFilterListV2(map[string]string{
			"prefix-list-name": v.(string),
		})...)
	}

	if v, ok := d.GetOk("prefix_list_id"); ok {
		input.PrefixListIds = []string{v.(string)}
	}

	input.Filters = append(input.Filters, newCustomFilterListV2(
		d.Get("filter").(*schema.Set),
	)...)

	pl, err := FindPrefixList(ctx, conn, input)

	if err != nil {
		return sdkdiag.AppendFromErr(diags, tfresource.SingularDataSourceFindError("EC2 Prefix List", err))
	}

	d.SetId(aws.ToString(pl.PrefixListId))
	d.Set("cidr_blocks", pl.Cidrs)
	d.Set("name", pl.PrefixListName)

	return diags
}

func FindPrefixList(ctx context.Context, conn *ec2.Client, input *ec2.DescribePrefixListsInput) (*awstypes.PrefixList, error) {
	output, err := FindPrefixLists(ctx, conn, input)

	if err != nil {
		return nil, err
	}

	return tfresource.AssertSingleValueResult(output)
}

func FindPrefixLists(ctx context.Context, conn *ec2.Client, input *ec2.DescribePrefixListsInput) ([]awstypes.PrefixList, error) {
	var output []awstypes.PrefixList

	paginatior := ec2.NewDescribePrefixListsPaginator(conn, input)
	for paginatior.HasMorePages() {
		page, err := paginatior.NextPage(ctx)

		if tfawserr.ErrCodeEquals(err, errCodeInvalidPrefixListIdNotFound) {
			return nil, &retry.NotFoundError{
				LastError:   err,
				LastRequest: input,
			}
		}

		if err != nil {
			return nil, err
		}

		output = append(output, page.PrefixLists...)
	}

	return output, nil
}
