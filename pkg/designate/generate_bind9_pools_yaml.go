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
package designate

import (
	"bytes"
	"fmt"
	"text/template"
)

// PoolData represents the data GeneratePoolsYamlFile will receive from the controller
type PoolData struct {
	MdnsMap    map[string]string
	BindMap    map[string]string
	Attributes map[string]string
}

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

func GeneratePoolsYamlFile(pooslData []PoolData) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("\n---\n")

	for _, poolData := range pooslData {
		// Create ns_records
		nsRecords := []NSRecord{}
		priority := 1

		for i := 0; i < 3; i++ { // TODO oschwart: implement ns_records part - I chose 3 randomly
			nsRecord := NSRecord{
				Hostname: fmt.Sprintf("ns%d.example.org.", priority),
				Priority: priority,
			}
			nsRecords = append(nsRecords, nsRecord)
			priority++
		}

		// As far as I understand, we will only have one mdnsIP (primary DNS server)
		var mdnsIP string
		for _, v := range poolData.MdnsMap {
			mdnsIP = v
			break
		}

		// Create targets and nameservers
		nameservers := []Nameserver{}
		targets := []Target{}
		rndcKeyNum := 1

		for _, bindIP := range poolData.BindMap {
			nameservers = append(nameservers, Nameserver{
				Host: bindIP,
				Port: 53,
			})

			target := Target{
				Type:        "bind9",
				Description: fmt.Sprintf("BIND9 Server %d (%s)", rndcKeyNum, bindIP),
				Masters: []Master{
					{
						Host: mdnsIP,
						Port: 16000, // TODO oschwart: change it to the real value
					},
					{
						Host: mdnsIP,
						Port: 16001, // TODO oschwart: change it to the real value
					},
					{
						Host: mdnsIP,
						Port: 16002, // TODO oschwart: change it to the real value
					},
				},
				Options: Options{
					Host:           bindIP,
					Port:           53,
					RNDCHost:       bindIP,
					RNDCPort:       953,
					RNDCConfigFile: fmt.Sprintf("%s/%s-%d.conf", RndcConfDir, DesignateRndcKey, rndcKeyNum),
				},
			}
			targets = append(targets, target)
			rndcKeyNum++
		}

		// Catalog zone is an optional section
		// catalogZone := &CatalogZone{
		// 	FQDN:    "example.org.",
		// 	Refresh: "60",
		// }

		// Create the Pool struct with the dynamic values
		pool := Pool{
			Name:        "default",
			Description: "DevStack BIND Pool",
			Attributes:  poolData.Attributes,
			NSRecords:   nsRecords,
			Nameservers: nameservers,
			Targets:     targets,
			CatalogZone: nil, // set to catalogZone if this section should be presented
		}

		// Parse the template and return it as a string
		tmpl, err := template.New("pool").Parse(poolsTemplate)
		if err != nil {
			return "", err
		}

		err = tmpl.Execute(&buf, pool)
		if err != nil {
			return "", err
		}
	}

	return buf.String(), nil
}

const poolsTemplate = `
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
