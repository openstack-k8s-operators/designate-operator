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

func GetCommonScriptsSecretName(parent client.Object) string {
	return fmt.Sprintf(CommonScriptsTemplate, GetOwningDesignateName(parent))
}

func GetCommonConfigSecretName(parent client.Object) string {
	return fmt.Sprintf(CommonConfigDataTemplate, GetOwningDesignateName(parent))
}

func GetCommonDefaultOverwritesName(parent client.Object) string {
	return fmt.Sprintf(CommonDefaultOverwriteTemplate, GetOwningDesignateName(parent))
}

func GetCommonSecretNames(instance client.Object) []string {
	owner := GetOwningDesignateName(instance)
	if len(owner) > 0 {
		return []string{
			fmt.Sprintf(CommonScriptsTemplate, owner),
			fmt.Sprintf(CommonConfigDataTemplate, owner),
			fmt.Sprintf(CommonDefaultOverwriteTemplate, owner),
		}
	}
	return []string{}
}
