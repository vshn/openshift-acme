package builder

import (
	"errors"
	"fmt"
	"context"
	"crypto/x509"
	"encoding/pem"

	corev1 "k8s.io/api/core/v1"
	acmelib "golang.org/x/crypto/acme"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	acmeclient "github.com/tnozicka/openshift-acme/pkg/acme/client"
	"github.com/golang/glog"
)

const (
	AnnotationAcmeAccountContactsKey = "kubernetes.io/acme.account-contacts"
	DataAcmeAccountUrlKey            = "acme.account-url"
	DataTlslKey                      = "tls.key"
	LabelAcmeTypeKey                 = "kubernetes.io/acme.type"
	LabelAcmeAccountType             = "account"
)

var (
	LabelSelectorAcmeAccount = fmt.Sprintf("%s=%s", LabelAcmeTypeKey, LabelAcmeAccountType)
)

type SecretNamespaceGetter interface {
	Get(name string) (*corev1.Secret, error)
}

type SecretGetter interface {
	Secrets(namespace string) SecretNamespaceGetter
}

func BuildClientFromSecret(secret *corev1.Secret, acmeUrl string) (*acmeclient.Client, error) {
	if secret.Data == nil {
		return nil, errors.New("malformed acme account: missing Data")
	}

	keyPem, ok := secret.Data[DataTlslKey]
	if !ok {
		return nil, fmt.Errorf("malformed acme account: missing Data.'%s'", DataTlslKey)
	}
	urlBytes, ok := secret.Data[DataAcmeAccountUrlKey]
	if !ok {
		return nil, fmt.Errorf("malformed acme account: missing Data.'%s'", DataAcmeAccountUrlKey)
	}
	url := string(urlBytes)

	block, _ := pem.Decode(keyPem)
	if block == nil {
		return nil, errors.New("existing account has invalid PEM encoded private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	return &acmeclient.Client{
		Account: &acmelib.Account{
			URI: url,
		},
		Client: &acmelib.Client{
			Key: key,
			DirectoryURL: acmeUrl,
		},
	}, nil
}

type clientFactory struct {
	acmeUrl string
	secretName string
	secretNamespace string
	kubeClientset kubernetes.Interface
	secretGetter SecretGetter
}

func NewClientFactory(acmeUrl, secretName, secretNamespace string, kubeClientset kubernetes.Interface, secretGetter SecretGetter) *clientFactory {
	return &clientFactory{
		acmeUrl: acmeUrl,
		secretName: secretName,
		secretNamespace: secretNamespace,
		kubeClientset: kubeClientset,
		secretGetter: secretGetter,
	}
}

func (f *clientFactory) newAccountClient(ctx context.Context) (*acmeclient.Client, error) {
	account := &acmelib.Account{

	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: f.secretName,

		},
		//Data:
	}
}

func (f *clientFactory) newAccountSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: f.secretName,

		},
		//Data:
	}
}

func (f *clientFactory) GetClient(ctx context.Context) (*acmeclient.Client, error) {
	secret, err := f.secretGetter.Secrets(f.secretNamespace).Get(f.secretName)
	if err != nil {
		if !kapierrors.IsNotFound(err) {
			return nil, err
		}

		secret, err = f.kubeClientset.CoreV1().Secrets(f.secretNamespace).Create(f.newAccountSecret())
		if err != nil {
			if !kapierrors.IsAlreadyExists(err) {
				return nil, err
			}
		} else {
		glog.Infof("Created new ACME account %s/%s", f.secretNamespace, f.secretName)

		}
	}

	// TODO: if the account has empty URL register new one with the key

	return BuildClientFromSecret(secret, f.acmeUrl)
}