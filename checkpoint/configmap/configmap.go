package configmap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/config/diag"
	"github.com/shibernetes/kem-agent/internal/version"
)

// TypeName is the value of the type key that selects this implementation.
const (
	TypeName = "configmap"
)

const (
	dataKey = "state.json"
)

var _ checkpoint.Store = (*Store)(nil)

// A Store persists a JSON-encoded checkpoint state in one data key of a
// Kubernetes ConfigMap. A single writer makes a plain update sufficient,
// with no need for server-side apply.
type Store struct {
	client    kubernetes.Interface
	namespace string
	name      string
}

// Config defines the configuration for a [Store].
type Config struct {
	Type      string `yaml:"type"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{Type: TypeName}
}

// Validate validates the configuration.
func (c Config) Validate() error {
	if err := diag.Required(c); err != nil {
		return err
	}
	if errs := validation.IsDNS1123Subdomain(c.Name); len(errs) > 0 {
		return diag.Pathf("name", "invalid name %q: %s", c.Name, strings.Join(errs, ", "))
	}
	if c.Namespace == "" {
		// When the namespace is not explicitly set, it uses the agent
		// own namespace by default.
		return nil
	}
	if errs := validation.IsDNS1123Label(c.Namespace); len(errs) > 0 {
		return diag.Pathf("namespace", "invalid namespace %q: %s", c.Namespace, strings.Join(errs, ", "))
	}
	return nil
}

// New returns a store backed by a Kubernetes ConfigMap.
func New(client kubernetes.Interface, cfg Config) *Store {
	return &Store{
		client:    client,
		namespace: cfg.Namespace,
		name:      cfg.Name,
	}
}

// Load reads and decodes the state. It returns [checkpoint.ErrNotFound]
// when the ConfigMap does not exist, and [checkpoint.ErrCorrupt] when it
// exists without holding a state.
func (s *Store) Load(ctx context.Context) (checkpoint.State, error) {
	obj, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return checkpoint.State{}, checkpoint.ErrNotFound
	}
	if err != nil {
		return checkpoint.State{}, fmt.Errorf("failed to get configmap %s/%s: %w", s.namespace, s.name, err)
	}
	data, ok := obj.Data[dataKey]
	if !ok {
		return checkpoint.State{}, fmt.Errorf("%w: no %q key", checkpoint.ErrCorrupt, dataKey)
	}
	var state checkpoint.State

	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return checkpoint.State{}, fmt.Errorf("%w: %w", checkpoint.ErrCorrupt, err)
	}
	return state, nil
}

// Save writes the state, creating the ConfigMap on first use and updating
// it thereafter.
//
// A conflict indicates that the resource version the update carried is
// stale, so the update is retried.
func (s *Store) Save(ctx context.Context, state checkpoint.State) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode checkpoint state: %w", err)
	}
	c := s.client.CoreV1().ConfigMaps(s.namespace)

	// The retry identifies a conflict by inspecting the original
	// error, so it is returned as-is, unwrapped.
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := c.Get(ctx, s.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = c.Create(ctx, &corev1.ConfigMap{
				Name:      s.name,
				Namespace: s.namespace,
				Data:      map[string]string{dataKey: string(b)},
			}, metav1.CreateOptions{
				FieldManager: version.Name,
			})
			return err
		}
		if err != nil {
			return err
		}
		if obj.Data == nil {
			obj.Data = make(map[string]string, 1)
		}
		obj.Data[dataKey] = string(b)

		_, err = c.Update(ctx, obj, metav1.UpdateOptions{
			FieldManager: version.Name,
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to save checkpoint %s/%s: %w", s.namespace, s.name, err)
	}
	return nil
}
