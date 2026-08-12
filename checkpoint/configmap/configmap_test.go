package configmap

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/shibernetes/kem-agent/checkpoint"
	"github.com/shibernetes/kem-agent/internal/version"
)

const (
	storeName      = "kem-agent-checkpoint"
	storeNamespace = "kem-system"
)

func TestConfigAccepts(t *testing.T) {
	cases := map[string]Config{
		"name and a namespace": {Type: TypeName, Name: storeName, Namespace: storeNamespace},
		"name alone":           {Type: TypeName, Name: storeName},
		"dotted name":          {Type: TypeName, Name: "kem-agent.checkpoint"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err != nil {
				t.Errorf("the configuration was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejectsMalformedNames(t *testing.T) {
	cases := map[string]Config{
		"spaced name":      {Type: TypeName, Name: "Bad Name"},
		"uppercase name":   {Type: TypeName, Name: "Checkpoint"},
		"underscored name": {Type: TypeName, Name: "kem_agent"},
		"spaced namespace": {Type: TypeName, Name: storeName, Namespace: "Bad NS"},
		"dotted namespace": {Type: TypeName, Name: storeName, Namespace: "kem.system"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]Config{
		"nothing":         {},
		"namespace alone": {Namespace: storeNamespace},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("the configuration was accepted, want it rejected")
			}
		})
	}
}

func TestLoadReturnsState(t *testing.T) {
	var (
		want   = newState()
		client = fake.NewClientset(configMapWith(t, want))
	)
	state, err := New(client, config()).Load(context.Background())
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	if !maps.Equal(state.Watches, want.Watches) {
		t.Errorf("got watches %v, want %v", state.Watches, want.Watches)
	}
}

func TestLoadReportsMissingConfigMap(t *testing.T) {
	_, err := New(fake.NewClientset(), config()).Load(context.Background())
	if !errors.Is(err, checkpoint.ErrNotFound) {
		t.Errorf("got error %v, want ErrNotFound", err)
	}
}

func TestLoadReportsMissingDataKey(t *testing.T) {
	client := fake.NewClientset(&corev1.ConfigMap{
		Name: storeName, Namespace: storeNamespace,
	})
	_, err := New(client, config()).Load(context.Background())
	if !errors.Is(err, checkpoint.ErrCorrupt) {
		t.Errorf("got error %v, want ErrCorrupt", err)
	}
}

func TestLoadReportsUnreadableState(t *testing.T) {
	client := fake.NewClientset(configMap("{not json"))

	_, err := New(client, config()).Load(context.Background())
	if !errors.Is(err, checkpoint.ErrCorrupt) {
		t.Errorf("got error %v, want ErrCorrupt", err)
	}
}

func TestLoadFailsOnForbiddenRead(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("get", "configmaps", forbidden())

	_, err := New(client, config()).Load(context.Background())
	if err == nil {
		t.Fatal("got no error, want a forbidden read to fail")
	}
	// The positions may be intact, so a refused read must not
	// return an error that would be interpreted as a missing
	// or corrupt configmap.
	if errors.Is(err, checkpoint.ErrNotFound) || errors.Is(err, checkpoint.ErrCorrupt) {
		t.Errorf("got error %v, want it reported as neither missing nor unreadable", err)
	}
}

func TestSaveFailsOnForbiddenWrite(t *testing.T) {
	client := fake.NewClientset(configMapWith(t, current()))
	client.PrependReactor("update", "configmaps", forbidden())

	if err := New(client, config()).Save(context.Background(), newState()); err == nil {
		t.Error("got no error, want a forbidden write to fail")
	}
}

func TestSaveWritesState(t *testing.T) {
	cases := map[string]struct {
		existing []runtime.Object
		verb     string
	}{
		"no configmap":       {verb: "create"},
		"existing configmap": {existing: []runtime.Object{configMapWith(t, current())}, verb: "update"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client := fake.NewClientset(tc.existing...)

			if err := New(client, config()).Save(context.Background(), newState()); err != nil {
				t.Fatalf("failed to save state: %v", err)
			}
			if n := countActionForVerb(client, tc.verb); n != 1 {
				t.Errorf("got %d %s calls, want 1", n, tc.verb)
			}
			if got, want := storedWatches(t, client), newState().Watches; !maps.Equal(got, want) {
				t.Errorf("got watches %v, want %v", got, want)
			}
		})
	}
}

func TestSaveRetriesOnConflict(t *testing.T) {
	client := fake.NewClientset(configMapWith(t, current()))

	var updates int
	client.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			return true, nil, apierrors.NewConflict(groupResource(), storeName, errors.New("stale resource version"))
		}
		return false, nil, nil
	})
	if err := New(client, config()).Save(context.Background(), newState()); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}
	if n := countActionForVerb(client, "update"); n != 2 {
		t.Errorf("got %d updates, want a retry on conflict", n)
	}
	// The retry must read the state again, or it reapplies the
	// same resource version the APIServer just refused.
	if n := countActionForVerb(client, "get"); n != 2 {
		t.Errorf("got %d gets, want one before each update", n)
	}
	if got, want := storedWatches(t, client), newState().Watches; !maps.Equal(got, want) {
		t.Errorf("got watches %v, want %v", got, want)
	}
}

func TestSaveFieldManagerOption(t *testing.T) {
	var (
		client = fake.NewClientset()
		store  = New(client, config())
	)
	for range 2 {
		if err := store.Save(context.Background(), newState()); err != nil {
			t.Fatalf("failed to save state: %v", err)
		}
	}
	var actions int
	for _, action := range client.Actions() {
		switch a := action.(type) {
		case k8stesting.CreateActionImpl:
			actions++
			if a.CreateOptions.FieldManager != version.Name {
				t.Errorf("got create option field manager %q, want %q", a.CreateOptions.FieldManager, version.Name)
			}
		case k8stesting.UpdateActionImpl:
			actions++
			if a.UpdateOptions.FieldManager != version.Name {
				t.Errorf("got update option field manager %q, want %q", a.UpdateOptions.FieldManager, version.Name)
			}
		}
	}
	if actions != 2 {
		t.Errorf("got %d actions, want the create and update", actions)
	}
}

func config() Config {
	return Config{Name: storeName, Namespace: storeNamespace}
}

func newState() checkpoint.State {
	return checkpoint.State{Watches: map[string]string{"": "100", "team-a": "42"}}
}

// current returns the state a store already holds before a save.
func current() checkpoint.State {
	return checkpoint.State{Watches: map[string]string{"team-a": "1"}}
}

func configMapWith(t *testing.T, state checkpoint.State) *corev1.ConfigMap {
	t.Helper()

	b, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to encode state: %v", err)
	}
	return configMap(string(b))
}

func configMap(data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		Name: storeName, Namespace: storeNamespace,
		Data: map[string]string{dataKey: data},
	}
}

func groupResource() schema.GroupResource {
	return schema.GroupResource{Resource: "configmaps"}
}

// forbidden answers every call it is prepended to with a forbidden error.
func forbidden() k8stesting.ReactionFunc {
	return func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(groupResource(), storeName, errors.New("forbidden"))
	}
}

func storedWatches(t *testing.T, client *fake.Clientset) map[string]string {
	t.Helper()

	gvr := schema.GroupVersionResource{
		Version:  "v1",
		Resource: "configmaps",
	}
	// The tracker is the store the client writes through, so reading
	// it directly records no action a test counting them would have
	// to account for.
	obj, err := client.Tracker().Get(gvr, storeNamespace, storeName)
	if err != nil {
		t.Fatalf("failed to get configmap: %v", err)
	}
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("got object type %T, want ConfigMap", obj)
	}
	var state checkpoint.State

	if err := json.Unmarshal([]byte(cm.Data[dataKey]), &state); err != nil {
		t.Fatalf("failed to decode state: %v", err)
	}
	return state.Watches
}

func countActionForVerb(client *fake.Clientset, verb string) int {
	var n int
	for _, action := range client.Actions() {
		if action.GetVerb() == verb {
			n++
		}
	}
	return n
}
