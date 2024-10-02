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
	"os"
	"path"
	"text/template"

	"gopkg.in/yaml.v2"

	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
)

type Pool struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Attributes  map[string]string `yaml:"attributes"`
	NSRecords   []NSRecord        `yaml:"ns_records"`
	Nameservers []Nameserver      `yaml:"nameservers"`
	Targets     []Target          `yaml:"targets"`
	CatalogZone *CatalogZone      `yaml:"catalog_zone,omitempty"` // it is a pointer because it is optional
}

type NSRecord struct {
	Hostname string `yaml:"hostname"`
	Priority int    `yaml:"priority"`
}

type Nameserver struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type Target struct {
	Type        string   `yaml:"type"`
	Description string   `yaml:"description"`
	Masters     []Master `yaml:"masters"`
	Options     Options  `yaml:"options"`
}

type Master struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type Options struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	RNDCHost       string `yaml:"rndc_host"`
	RNDCPort       int    `yaml:"rndc_port"`
	RNDCConfigFile string `yaml:"rndc_config_file"`
}

type CatalogZone struct {
	FQDN    string `yaml:"fqdn"`
	Refresh int    `yaml:"refresh"`
}

func GeneratePoolsYamlData(BindMap, MdnsMap, NsRecordsMap map[string]string) (string, error) {
	// Create ns_records
	nsRecords := []NSRecord{}
	for _, data := range NsRecordsMap {
		err := yaml.Unmarshal([]byte(data), &nsRecords)
		if err != nil {
			return "", fmt.Errorf("error unmarshalling yaml: %w", err)
		}
	}

	// Create targets and nameservers
	nameservers := []Nameserver{}
	targets := []Target{}
	rndcKeyNum := 1

	for _, bindIP := range BindMap {
		nameservers = append(nameservers, Nameserver{
			Host: bindIP,
			Port: 53,
		})

		masters := []Master{}
		for _, masterHost := range MdnsMap {
			masters = append(masters, Master{
				Host: masterHost,
				Port: 5354,
			})
		}

		target := Target{
			Type:        "bind9",
			Description: fmt.Sprintf("BIND9 Server %d (%s)", rndcKeyNum, bindIP),
			Masters:     masters,
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
	//	Refresh: 60,
	// }
	defaultAttributes := make(map[string]string)
	// Create the Pool struct with the dynamic values
	pool := Pool{
		Name:        "default",
		Description: "Default BIND Pool",
		Attributes:  defaultAttributes,
		NSRecords:   nsRecords,
		Nameservers: nameservers,
		Targets:     targets,
		CatalogZone: nil, // set to catalogZone if this section should be presented
	}

	opTemplates, err := util.GetTemplatesPath()
	if err != nil {
		return "", err
	}
	poolsYamlPath := path.Join(opTemplates, PoolsYamlPath)
	poolsYaml, err := os.ReadFile(poolsYamlPath)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("pool").Parse(string(poolsYaml))
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, pool)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}
