package kube

import (
	"context"
	"encoding/json"
	"fmt"

	k8sversion "k8s.io/apimachinery/pkg/util/version"
	apiversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
)

// The oldest APIServer the agent runs against. Below it a watch ignores
// the streaming list options rather than rejecting them, so the bookmark
// signaling the initial events end never arrives and no read position is
// ever recorded.
var minServerVersion = k8sversion.MustParseGeneric("1.27")

// CheckServerVersion returns the version the APIServer reports, and
// fails when it is older than the agent supports.
func CheckServerVersion(ctx context.Context, client kubernetes.Interface) (string, error) {
	// The typed discovery call ignores the context it is given,
	// so the request goes through the REST client instead.
	body, err := client.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return "", fmt.Errorf("failed to query the APIServer version: %w", err)
	}
	var info apiversion.Info

	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("failed to decode the APIServer version: %w", err)
	}
	// GitVersion is the field to read, the minor one being free-form and
	// reported as "27+" by some distributions. ParseGeneric accepts the
	// leading v prefix and whatever vendor suffix follows the numbers.
	v, err := k8sversion.ParseGeneric(info.GitVersion)
	if err != nil {
		return "", fmt.Errorf("failed to parse the APIServer version %q: %w", info.GitVersion, err)
	}
	if v.LessThan(minServerVersion) {
		return "", fmt.Errorf(
			"APIServer version %s is older than the minimum supported: %s",
			info.GitVersion, minServerVersion,
		)
	}
	return info.GitVersion, nil
}
