package main

import "fmt"
import "text/template"

func main() {
	testTemplate := `[DEFAULT]
rpc_response_timeout = 60
quota_api_export_size = 1000
quota_recordset_records = 20
quota_zone_records = 500
quota_zone_recordsets = 500
quota_zones = 10
root-helper = sudo
state_path = /etc/designate/data
transport_url = {{ .TransportURL }}

[database]
connection = {{ .DatabaseConnection }}

[storage:sqlalchemy]
connection = {{ .DatabaseConnection }}

[service:api]
quotas_verify_project_id = True
auth_strategy = keystone
enable_api_admin = True
enable_api_v2 = True
enable_host_header = True
enabled_extensions_admin = quotas


[oslo_messaging_notifications]
topics = notifications
driver = messagingv2

[oslo_concurrency]
lock_path = /opt/stack/data/designate

[oslo_policy]
enforce_scope=False
enforce_new_defaults=False

[keystone_authtoken]
www_authenticate_uri = {{ .KeystonePublicURL }}
auth_url = {{ .KeystoneInternalURL }}
username = {{ .ServiceUser }}
project_name = service
project_domain_name = Default
user_domain_name = Default
auth_type = password
password = {{ .AdminPassword }}
region_name = regionOne
interface = internal
`
	_, err := template.New("").Parse(testTemplate)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println("yay")
	}
}
