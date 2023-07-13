package designate

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
