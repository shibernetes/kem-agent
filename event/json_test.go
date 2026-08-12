package event

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"flag"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const goldenFile = "testdata/event.golden"

var update = flag.Bool("update", false, "rewrite the golden file")

func TestMarshalJSONTo(t *testing.T) {
	got, err := jsonv2.Marshal(fullEvent(), jsontext.WithIndent("  "))
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}
	if *update {
		if err := os.WriteFile(goldenFile, got, 0o644); err != nil {
			t.Fatalf("failed to write the golden file: %v", err)
		}
	}
	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("failed to read the golden file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// The cases the golden file cannot show, each one a field whose absence or
// null form matters.
func TestMarshalJSONToEdgeCases(t *testing.T) {
	cases := map[string]struct {
		event Event
		want  string
	}{
		"a zero event time is null": {
			event: Event{},
			want:  `{"eventTime":null,"regarding":{}}`,
		},
		"empty maps are omitted": {
			event: Event{ObjectMeta: ObjectMeta{Labels: map[string]string{}}},
			want:  `{"eventTime":null,"regarding":{}}`,
		},
		"a series with no observation": {
			event: Event{Series: &eventsv1.EventSeries{Count: 2}},
			want:  `{"eventTime":null,"series":{"count":2,"lastObservedTime":null},"regarding":{}}`,
		},
		"an unterminating enriched object": {
			event: Event{RegardingObject: &RegardingObject{}},
			want:  `{"eventTime":null,"regarding":{},"regardingObject":{}}`,
		},
		"a time is written in UTC": {
			event: Event{
				EventTime: metav1.NewMicroTime(time.Date(2009, 1, 3, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60))),
			},
			want: `{"eventTime":"2009-01-03T10:00:00.000000Z","regarding":{}}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := jsonv2.Marshal(tc.event)
			if err != nil {
				t.Fatalf("failed to encode: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func fullEvent() Event {
	observed := time.Date(2009, 1, 3, 10, 30, 15, 123456000, time.UTC)

	return Event{
		UID:             "8f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f",
		ResourceVersion: "48291",
		Namespace:       "team-a",
		Name:            "web-7d9f.17c8e2a1b4f5",
		Labels:          map[string]string{"tier": "front", "app": "web"},
		Annotations:     map[string]string{"kubernetes.io/change-cause": "rollout"},
		EventTime:       metav1.NewMicroTime(observed.Add(-time.Minute)),
		Series: &eventsv1.EventSeries{
			Count:            7,
			LastObservedTime: metav1.NewMicroTime(observed),
		},
		ReportingController: "kubelet",
		ReportingInstance:   "kubelet-node-1",
		Action:              "Pulling",
		Reason:              "BackOff",
		Regarding: corev1.ObjectReference{
			Kind:            "Pod",
			Namespace:       "team-a",
			Name:            "web-7d9f",
			UID:             "1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d",
			APIVersion:      "v1",
			ResourceVersion: "48277",
			FieldPath:       "spec.containers{web}",
		},
		Related: &corev1.ObjectReference{
			Kind:       "ConfigMap",
			Namespace:  "team-a",
			Name:       "web-config",
			UID:        "2b3c4d5e-6f7a-8b9c-0d1e-2f3a4b5c6d7e",
			APIVersion: "v1",
		},
		Note: `Back-off restarting failed container web, last state <nil> & "unknown"`,
		Type: "Warning",
		RegardingObject: &RegardingObject{
			Labels:      map[string]string{"app": "web"},
			Annotations: map[string]string{"cni.projectcalico.org/podIP": "10.1.2.3/32"},
			Owner: &corev1.ObjectReference{
				Kind:       "ReplicaSet",
				Namespace:  "team-a",
				Name:       "web-7d9f4c",
				UID:        "3c4d5e6f-7a8b-9c0d-1e2f-3a4b5c6d7e8f",
				APIVersion: "apps/v1",
			},
			Terminating: true,
		},
	}
}
