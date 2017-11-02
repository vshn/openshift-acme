package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tnozicka/openshift-acme/pkg/api"
)

func IsManaged(obj metav1.Object) bool {
	annotation, ok := obj.GetAnnotations()[api.TlsAcmeAnnotation]
	if !ok {
		return false
	}

	return annotation == "true"
}