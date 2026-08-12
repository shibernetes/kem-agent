package sinks

import (
	"github.com/shibernetes/kem-agent/sink"
	"github.com/shibernetes/kem-agent/sink/file"
	"github.com/shibernetes/kem-agent/sink/graylog"
	"github.com/shibernetes/kem-agent/sink/metrics"
	"github.com/shibernetes/kem-agent/sink/otel"
	"github.com/shibernetes/kem-agent/sink/stdout"
	"github.com/shibernetes/kem-agent/sink/webhook"
)

// Factories returns every sink implementation the agent can build, keyed type name.
func Factories() sink.Factories {
	return sink.Factories{
		file.TypeName:    file.NewFactory(),
		graylog.TypeName: graylog.NewFactory(),
		metrics.TypeName: metrics.NewFactory(),
		otel.TypeName:    otel.NewFactory(),
		stdout.TypeName:  stdout.NewFactory(),
		webhook.TypeName: webhook.NewFactory(),
	}
}
