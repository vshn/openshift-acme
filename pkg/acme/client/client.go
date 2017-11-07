package client

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"sync"

	"github.com/golang/glog"
	"github.com/tnozicka/openshift-acme/pkg/cert"
	"golang.org/x/crypto/acme"
)

func acceptTerms(tosURL string) bool {
	glog.Infof("By continuing running this program you agree to the CA's Terms of Service (%s). If you do not agree exit the program immediately!", tosURL)
	return true
}

// Has to support concurrent calls
type ChallengeExposer interface {
	// Exposes challenge
	Expose(c *acme.Client, domain string, token string) error

	// Removes challenge
	Remove(c *acme.Client, domain string, token string) error
}

type Client struct {
	Client  *acme.Client
	Account *acme.Account
}

func (c *Client) CreateAccount(ctx context.Context, a *acme.Account) error {
	var err error
	c.Client.Key, err = rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}

	c.Account, err = c.Client.Register(ctx, a, acceptTerms)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DeactivateAccount(ctx context.Context, a *acme.Account) error {
	return c.Client.RevokeAuthorization(ctx, a.URI)
}

func getStatisfiableCombinations(authorization *acme.Authorization, exposers map[string]ChallengeExposer) [][]int {
	combinations := [][]int{}

	for _, combination := range authorization.Combinations {
		satisfiable := true
		for _, challengeId := range combination {
			if challengeId >= len(authorization.Challenges) {
				glog.Warning("ACME authorization has contains challengeId %d out of range; %#v", challengeId, authorization)
				satisfiable = false
				continue
			}

			if _, ok := exposers[authorization.Challenges[challengeId].Type]; !ok {
				satisfiable = false
				continue
			}
		}

		if satisfiable {
			combinations = append(combinations, combination)
		}
	}

	return combinations
}

func (c *Client) AcceptAuthorization(ctx context.Context, authorization *acme.Authorization, exposers map[string]ChallengeExposer) (*acme.Authorization, error) {
	glog.V(4).Infof("Found %d possible combinations for authorization", len(authorization.Combinations))

	combinations := getStatisfiableCombinations(authorization, exposers)
	if len(combinations) == 0 {
		return nil, fmt.Errorf("none of %d combination could be satified", len(authorization.Combinations))
	}

	glog.V(4).Infof("Found %d valid combinations for authorization", len(combinations))

	// TODO: sort combinations by preference

	// TODO: consider using the remaining combinations if this one fails
	combination := combinations[0]
	for _, challengeId := range combination {
		challenge := authorization.Challenges[challengeId]

		exposer, ok := exposers[challenge.Type]
		if !ok {
			return nil, errors.New("internal error: unavailable exposer")
		}

		err = exposer.Expose(c.Client, domain, challenge.Token)
		if err != nil {
			return nil, err
		}

		challenge, err = c.Client.Accept(ctx, challenge)
		if err != nil {
			return nil, err
		}
	}

	authorization, err = c.Client.GetAuthorization(ctx, authorization.URI)
	if err != nil {
		return nil, err
	}

	return authorization, nil
}

type FailedDomain struct {
	Domain string
	Err    error
}

func (d FailedDomain) String() string {
	return fmt.Sprintf("domain: %s, error: %s", d.Domain, d.Err)
}

type DomainsAuthorizationError struct {
	FailedDomains []FailedDomain
}

func (e DomainsAuthorizationError) Error() (res string) {
	return fmt.Sprint(e.FailedDomains)
}

func (c *Client) ObtainCertificate(ctx context.Context, domains []string, exposers map[string]ChallengeExposer, onlyForAllDomains bool) (certificate *cert.CertPemData, err error) {
	//defer log.Trace("acme.Client ObtainCertificate").End()
	var wg sync.WaitGroup
	results := make([]error, len(domains))
	for i, domain := range domains {
		wg.Add(1)
		go func(i int, domain string) {
			defer wg.Done()
			_, err := c.ValidateDomain(ctx, domain, exposers)
			results[i] = err
		}(i, domain)
	}
	wg.Wait()
	glog.V(4).Info("finished validating domains")

	validatedDomains := []string{}
	var domainsError DomainsAuthorizationError
	for i, err := range results {
		if err == nil {
			validatedDomains = append(validatedDomains, domains[i])
		} else {
			domainsError.FailedDomains = append(domainsError.FailedDomains, FailedDomain{Domain: domains[i], Err: err})
		}
	}

	if len(validatedDomains) == 0 {
		return nil, domainsError
	}

	if onlyForAllDomains && len(domainsError.FailedDomains) != 0 {
		return nil, domainsError
	}

	domains = validatedDomains

	template := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: domains[0],
		},
	}
	if len(domains) > 1 {
		template.DNSNames = domains
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return
	}

	csr, err := x509.CreateCertificateRequest(rand.Reader, &template, privateKey)
	if err != nil {
		return
	}

	der, _, err := c.Client.CreateCert(ctx, csr, 0, true)
	if err != nil {
		return
	}

	certificate, err = cert.NewCertificateFromDER(der, privateKey)
	if err != nil {
		return
	}

	return
}
