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
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

var (
	// ErrExternalBindAddressRequired is returned when an external bind entry omits address.
	ErrExternalBindAddressRequired = errors.New("address is required for external bind")
	// ErrExternalBindRndcSecretRequired is returned when an external bind entry omits rndcsecret.
	ErrExternalBindRndcSecretRequired = errors.New("rndcsecret is required for external bind")
)

// ExternalBind defines connection parameters for a BIND9 instance not managed by this operator.
type ExternalBind struct {
	Name          string `yaml:"name"`
	Address       string `yaml:"address"`
	Port          int    `yaml:"port"`
	RndcHost      string `yaml:"rndchost"`
	RndcKeyName   string `yaml:"rndckeyname"`
	RndcAlgorithm string `yaml:"rndcalgorithm"`
	RndcSecret    string `yaml:"rndcsecret"`
	RndcPort      int    `yaml:"rndcport"`
	// RndcFile is set by the controller after ReadExternalBinds returns — callers must
	// populate it before passing ExternalBind to createExternalTargetAndNameserver.
	RndcFile string `yaml:"-"`
}

const (
	// DefaultRndcKeyName is the BIND9 key clause name used when rndckeyname is omitted.
	DefaultRndcKeyName = "rndc-key"
	// DefaultRndcAlgorithm is the TSIG algorithm used when rndcalgorithm is omitted.
	DefaultRndcAlgorithm = "hmac-sha256"
)

// ReadExternalBinds unmarshals a YAML array of ExternalBind entries and applies
// defaults for omitted fields: Port (53), RndcPort (953), RndcKeyName ("rndc-key"),
// and RndcAlgorithm ("hmac-sha256").
func ReadExternalBinds(data []byte) ([]ExternalBind, error) {
	externalBinds := []ExternalBind{}

	err := yaml.Unmarshal(data, &externalBinds)
	if err != nil {
		return nil, err
	}

	for i := range externalBinds {
		if externalBinds[i].Address == "" {
			return nil, fmt.Errorf("%w: %s", ErrExternalBindAddressRequired, externalBinds[i].Name)
		}
		if externalBinds[i].RndcSecret == "" {
			return nil, fmt.Errorf("%w: %s", ErrExternalBindRndcSecretRequired, externalBinds[i].Name)
		}
		if externalBinds[i].Port == 0 {
			externalBinds[i].Port = DNSPort
		}
		if externalBinds[i].RndcPort == 0 {
			externalBinds[i].RndcPort = RNDCPort
		}
		if externalBinds[i].RndcHost == "" {
			externalBinds[i].RndcHost = externalBinds[i].Address
		}
		if externalBinds[i].RndcKeyName == "" {
			externalBinds[i].RndcKeyName = DefaultRndcKeyName
		}
		if externalBinds[i].RndcAlgorithm == "" {
			externalBinds[i].RndcAlgorithm = DefaultRndcAlgorithm
		}
		if externalBinds[i].Name == "" {
			externalBinds[i].Name = fmt.Sprintf("external-bind9-%s", externalBinds[i].Address)
		}
	}

	return externalBinds, nil
}
