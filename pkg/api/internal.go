package api

const (
	ForwardingRouteSuffing = "acme-forwarder"
)

type AcmeState string

const (
	AcmeStateNeedsCert       = "NeedsCertificate"
	AcmeStateWaitingForAuthz = "WaitingForAuthz"
	AcmeStateOk              = "OK"
)
