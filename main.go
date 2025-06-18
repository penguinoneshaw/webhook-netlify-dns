package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	v1 "k8s.io/api/core/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"

	openApiRuntime "github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/netlify/open-api/go/models"
	"github.com/netlify/open-api/go/plumbing/operations"
	"github.com/netlify/open-api/go/porcelain"
)

var GroupName = os.Getenv("GROUP_NAME")

var netlify = porcelain.NewRetryable(porcelain.Default.Transport, nil, porcelain.DefaultRetryAttempts)

func buildNetlifyClientAuth(
	namespace string,
	client kubernetes.Clientset,
	secretKeySelector v1.SecretKeySelector,
) (openApiRuntime.ClientAuthInfoWriterFunc, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), secretKeySelector.Name, metav1.GetOptions{})

	if err != nil {
		return nil, err
	}

	data, ok := secret.Data[secretKeySelector.Key]

	if !ok {
		return nil, fmt.Errorf("specified key %s not found on secret %s/%s", secretKeySelector.Key, namespace, secretKeySelector.Name)
	}
	netlifyAuth := openApiRuntime.ClientAuthInfoWriterFunc(
		func(r openApiRuntime.ClientRequest, _ strfmt.Registry) error {
			if err := r.SetHeaderParam("User-Agent", "NetlifyDDNS"); err != nil {
				return err
			}
			if err := r.SetHeaderParam("Authorization", "Bearer "+string(data)); err != nil {
				return err
			}
			return nil
		},
	)
	return netlifyAuth, nil
}

func buildZoneId(zone string) string {
	return strings.ReplaceAll(strings.TrimSuffix(zone, "."), ".", "_")
}

func main() {
	if GroupName == "" {
		panic("GROUP_NAME must be specified")
	}

	// This will register our custom DNS provider with the webhook serving
	// library, making it available as an API under the provided GroupName.
	// You can register multiple DNS provider implementations with a single
	// webhook, where the Name() method will be used to disambiguate between
	// the different implementations.
	cmd.RunWebhookServer(GroupName,
		&netlifyDNSProviderSolver{},
	)
}

// customDNSProviderSolver implements the provider-specific logic needed to
// 'present' an ACME challenge TXT record for your own DNS provider.
// To do so, it must implement the `github.com/cert-manager/cert-manager/pkg/acme/webhook.Solver`
// interface.
type netlifyDNSProviderSolver struct {
	// If a Kubernetes 'clientset' is needed, you must:
	// 1. uncomment the additional `client` field in this structure below
	// 2. uncomment the "k8s.io/client-go/kubernetes" import at the top of the file
	// 3. uncomment the relevant code in the Initialize method below
	// 4. ensure your webhook's service account has the required RBAC role
	//    assigned to it for interacting with the Kubernetes APIs you need.
	client kubernetes.Clientset
}

// customDNSProviderConfig is a structure that is used to decode into when
// solving a DNS01 challenge.
// This information is provided by cert-manager, and may be a reference to
// additional configuration that's needed to solve the challenge for this
// particular certificate or issuer.
// This typically includes references to Secret resources containing DNS
// provider credentials, in cases where a 'multi-tenant' DNS solver is being
// created.
// If you do *not* require per-issuer or per-certificate configuration to be
// provided to your webhook, you can skip decoding altogether in favour of
// using CLI flags or similar to provide configuration.
// You should not include sensitive information here. If credentials need to
// be used by your provider here, you should reference a Kubernetes Secret
// resource and fetch these credentials using a Kubernetes clientset.
type netlifyDNSProviderConfig struct {
	// Change the two fields below according to the format of the configuration
	// to be decoded.
	// These fields will be set by users in the
	// `issuer.spec.acme.dns01.providers.webhook.config` field.

	// The API Access token
	APIAccesstokenSecretRef v1.SecretKeySelector `json:"apiAccessTokenSecretRef"`
}

// Name is used as the name for this DNS solver when referencing it on the ACME
// Issuer resource.
// This should be unique **within the group name**, i.e. you can have two
// solvers configured with the same Name() **so long as they do not co-exist
// within a single webhook deployment**.
// For example, `cloudflare` may be used as the name of a solver.
func (c *netlifyDNSProviderSolver) Name() string {
	return "netlify"
}

// Present is responsible for actually presenting the DNS record with the
// DNS provider.
// This method should tolerate being called multiple times with the same value.
// cert-manager itself will later perform a self check to ensure that the
// solver has correctly configured the DNS provider.
func (c *netlifyDNSProviderSolver) Present(ch *v1alpha1.ChallengeRequest) error {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}

	auth, err := buildNetlifyClientAuth(ch.ResourceNamespace, c.client, cfg.APIAccesstokenSecretRef)

	if err != nil {
		return err
	}
	zoneId := buildZoneId(ch.ResolvedZone)

	zone, err := netlify.Operations.GetDNSRecords(operations.NewGetDNSRecordsParams().WithZoneID(zoneId), auth)

	if err != nil {
		return err
	}
	record := &models.DNSRecordCreate{
		Type:     "TXT",
		Hostname: strings.TrimSuffix(strings.TrimSuffix(ch.ResolvedFQDN, zoneId), "."),
		Value:    ch.Key,
	}

	for _, existingRecord := range zone.Payload {
		if existingRecord.Hostname == record.Hostname && existingRecord.Value == record.Value {
			// Record with the correct name already exists
			return nil
		}
	}

	_, err = netlify.Operations.CreateDNSRecord(
		operations.NewCreateDNSRecordParams().WithZoneID(
			zoneId,
		).WithDNSRecord(record),
		auth,
	)

	if err != nil {
		return err
	}

	return nil
}

// CleanUp should delete the relevant TXT record from the DNS provider console.
// If multiple TXT records exist with the same record name (e.g.
// _acme-challenge.example.com) then **only** the record with the same `key`
// value provided on the ChallengeRequest should be cleaned up.
// This is in order to facilitate multiple DNS validations for the same domain
// concurrently.
func (c *netlifyDNSProviderSolver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}

	auth, err := buildNetlifyClientAuth(ch.ResourceNamespace, c.client, cfg.APIAccesstokenSecretRef)

	if err != nil {
		return err
	}

	zoneId := buildZoneId(ch.ResolvedZone)
	fqdn := strings.TrimSuffix(strings.TrimSuffix(ch.ResolvedFQDN, zoneId), ".")

	zone, err := netlify.Operations.GetDNSRecords(operations.NewGetDNSRecordsParams().WithZoneID(zoneId), auth)
	if err != nil {
		return err
	}

	for _, existingRecord := range zone.Payload {
		if existingRecord.Hostname == fqdn && existingRecord.Value == ch.Key {
			// Record with the correct name already exists
			_, err := netlify.Operations.DeleteDNSRecord(
				operations.NewDeleteDNSRecordParams().WithDNSRecordID(existingRecord.ID).WithZoneID(zoneId), auth)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Initialize will be called when the webhook first starts.
// This method can be used to instantiate the webhook, i.e. initialising
// connections or warming up caches.
// Typically, the kubeClientConfig parameter is used to build a Kubernetes
// client that can be used to fetch resources from the Kubernetes API, e.g.
// Secret resources containing credentials used to authenticate with DNS
// provider accounts.
// The stopCh can be used to handle early termination of the webhook, in cases
// where a SIGTERM or similar signal is sent to the webhook process.
func (c *netlifyDNSProviderSolver) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {

	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}

	c.client = *cl
	return nil
}

// loadConfig is a small helper function that decodes JSON configuration into
// the typed config struct.
func loadConfig(cfgJSON *extapi.JSON) (netlifyDNSProviderConfig, error) {
	cfg := netlifyDNSProviderConfig{}
	// handle the 'base case' where no configuration has been provided
	if cfgJSON == nil {
		return cfg, nil
	}
	if err := json.Unmarshal(cfgJSON.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("error decoding solver config: %v", err)
	}

	return cfg, nil
}
