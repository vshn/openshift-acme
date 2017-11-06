package api

const (
	ForwardingRouteSuffing = "acme_forwarder"
)

type AcmeState string

const (
	AcmeStateNeedsCert       = "NeedsCertificate"
	AcmeStateWaitingForAuthz = "WaitingForAuthz"
	AcmeStateOk              = "OK"
)
