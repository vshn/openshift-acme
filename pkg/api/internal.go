package api

const (
	ForwardingRouteSuffix = "acme"
	ExposerLabelName      = "acme.openshift.io/exposer"
	ExposerForLabelName   = "acme.openshift.io/exposer-for"
)

type AcmeState string

const (
	AcmeStateNeedsCert       = "NeedsCertificate"
	AcmeStateWaitingForAuthz = "WaitingForAuthz"
	AcmeStateOk              = "OK"
)
