package designate

import "fmt"
import "sigs.k8s.io/controller-runtime/pkg/client"

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

func ScriptsVolumeName(s string) string {
	return fmt.Sprintf(ScriptsF, s)
}

func ConfigVolumeName(s string) string {
	return fmt.Sprintf(ConfigDataTemplate, s)
}

func DefaultsVolumeName(s string) string {
	return fmt.Sprintf(DefaultOverwriteTemplate, s)
}

func MergedVolumeName(s string) string {
	return fmt.Sprintf("%s-merged", s)
}

func MergedDefaultsVolumeName(s string) string {
	return fmt.Sprintf("%s-merged-defaults", s)
}
