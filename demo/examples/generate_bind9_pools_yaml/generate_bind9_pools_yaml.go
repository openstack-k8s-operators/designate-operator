/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"os"
	"text/template"
)

type Pool struct {
	Name        string
	Description string
	Attributes  map[string]string
	NSRecords   []NSRecord
	Nameservers []Nameserver
	Targets     []Target
	CatalogZone *CatalogZone // it is a pointer because it is optional
}

type NSRecord struct {
	Hostname string
	Priority int
}

type Nameserver struct {
	Host string
	Port int
}

type Target struct {
	Type        string
	Description string
	Masters     []Master
	Options     Options
}

type Master struct {
	Host string
	Port int
}

type Options struct {
	Host           string
	Port           int
	RNDCHost       string
	RNDCPort       int
	RNDCConfigFile string
}

type CatalogZone struct {
	FQDN    string
	Refresh string
}

func main() {
	attributes := map[string]string{
		"pool_level": "default",
		"type":       "internal",
	}

	// Catalog zone is an optional section
	// catalogZone := &CatalogZone{
	// 	FQDN:    "example.org.",
	// 	Refresh: "60",
	// }

	pool := Pool{
		Name:        "default",
		Description: "DevStack BIND Pool",
		Attributes:  attributes,
		NSRecords: []NSRecord{
			{Hostname: "ns1.devstack.org.", Priority: 1},
		},
		Nameservers: []Nameserver{
			{Host: "192.168.124.114", Port: 53},
		},
		Targets: []Target{
			{
				Type:        "bind9",
				Description: "BIND Instance",
				Masters: []Master{
					{Host: "192.168.124.114", Port: 5354},
				},
				Options: Options{
					Host:           "192.168.124.114",
					Port:           53,
					RNDCHost:       "192.168.124.114",
					RNDCPort:       953,
					RNDCConfigFile: "/etc/named/rndc.conf",
				},
			},
		},
		CatalogZone: nil, // Set to catalogZone if this section should be presented
	}

	tmpl, err := template.New("pools").Parse(poolsTemplate)
	if err != nil {
		panic(err)
	}

	f, err := os.Create("pools.yaml")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	err = tmpl.Execute(f, pool)
	if err != nil {
		panic(err)
	}
}

const poolsTemplate = `
---
- name: {{.Name}}
  description: {{.Description}}
  attributes: {
    {{- range $key, $value := .Attributes }}
    "{{ $key }}": "{{ $value }}",
    {{- end }}
  }

  ns_records:
    {{- range .NSRecords }}
    - hostname: {{.Hostname}}
      priority: {{.Priority}}
    {{- end }}

  nameservers:
    {{- range .Nameservers }}
    - host: {{.Host}}
      port: {{.Port}}
    {{- end }}

  targets:
    {{- range .Targets }}
    - type: {{.Type}}
      description: {{.Description}}

      masters:
        {{- range .Masters }}
        - host: {{.Host}}
          port: {{.Port}}
        {{- end }}

      options:
        host: {{.Options.Host}}
        port: {{.Options.Port}}
        rndc_host: {{.Options.RNDCHost}}
        rndc_port: {{.Options.RNDCPort}}
        rndc_config_file: {{.Options.RNDCConfigFile}}
    {{- end }}

  {{- if .CatalogZone }}
  catalog_zone:
    catalog_zone_fqdn: {{.CatalogZone.FQDN}}
    catalog_zone_refresh: '{{.CatalogZone.Refresh}}'
  {{- end }}
`
