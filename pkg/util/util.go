package util

import (
	"strings"

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

func FirstNLines(s string, n int) string {
	if n < 1 {
		return ""
	}

	lines := strings.SplitN(s, "\n", n+1)
	c := len(lines)
	if c > n {
		c = n
	}

	return strings.Join(lines[:c], "\n")
}

func MaxNCharacters(s string, n int) string {
	if n < 1 {
		return ""
	}

	if len(s) <= n {
		return s
	}

	return s[:n]
}
