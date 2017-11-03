package api


type AcmeState string

const (
	AcmeStateOk = "OK"
	AcmeStateWaiting = "NeedsCertificate"
	AcmeStateNeedsCert = "NeedsCertificate"
)
