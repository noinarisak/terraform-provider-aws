resource "aws_xray_group" "test" {
{{- template "region" }}
  group_name        = var.rName
  filter_expression = "responsetime > 5"
{{- template "tags" . }}
}
