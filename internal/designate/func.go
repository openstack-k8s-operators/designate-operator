package designate

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetOwningDesignateName - Given a Designate-->API,Central,Worker,Mdns,Producer
// object, returning the parent Designate object that created it (if any)
func GetOwningDesignateName(instance client.Object) string {
	for _, ownerRef := range instance.GetOwnerReferences() {
		if ownerRef.Kind == "Designate" {
			return ownerRef.Name
		}
	}

	return ""
}

// ScriptsVolumeName returns the scripts volume name for the given service
func ScriptsVolumeName(s string) string {
	return fmt.Sprintf(ScriptsF, s)
}

// ConfigVolumeName returns the config volume name for the given service
func ConfigVolumeName(s string) string {
	return fmt.Sprintf(ConfigDataTemplate, s)
}

// DefaultsVolumeName returns the defaults volume name for the given service
func DefaultsVolumeName(s string) string {
	return fmt.Sprintf(DefaultOverwriteTemplate, s)
}

// MergedVolumeName returns the merged volume name for the given service
func MergedVolumeName(s string) string {
	return fmt.Sprintf("%s-merged", s)
}

// MergedDefaultsVolumeName returns the merged defaults volume name for the given service
func MergedDefaultsVolumeName(s string) string {
	return fmt.Sprintf("%s-merged-defaults", s)
}
