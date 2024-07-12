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

const (
	// ServiceType -
	ServiceType = "dns"
	// ServiceName -
	ServiceName = "designate"
	// DatabaseName -
	DatabaseName = "designate"

	// DatabaseCRName - Name of the MariaDBDatabase CR
	DatabaseCRName = "designate"

	// DatabaseUsernamePrefix - used by EnsureMariaDBAccount when a new username
	// is to be generated, e.g. "designate_e5a4", "designate_78bc", etc
	DatabaseUsernamePrefix = "designate"

	// DesignatePublicPort -
	DesignatePublicPort int32 = 9001
	// DesignateInternalPort -
	DesignateInternalPort int32 = 9001
)
