package kafka

import (
	"strings"
	"testing"
	"time"

	"github.com/shibernetes/kem-agent/config/units"
	"github.com/shibernetes/kem-agent/sink"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.TLS.Insecure {
		t.Error("the default configuration connects in plaintext, want TLS")
	}
	if cfg.RequiredAcks != RequiredAcksAll {
		t.Errorf("got required acks %q, want %q", cfg.RequiredAcks, RequiredAcksAll)
	}
	if cfg.AllowAutoTopicCreation {
		t.Error("the default configuration allows the brokers to create the topic, want it refused")
	}
}

func TestConfigAccepts(t *testing.T) {
	cases := map[string]func(*Config){
		"defaults":               func(*Config) {},
		"several brokers":        func(c *Config) { c.Brokers = []string{"kafka-0:9092", "kafka-1:9092"} },
		"IPv6 broker":            func(c *Config) { c.Brokers = []string{"[2001:db8::1]:9092"} },
		"longest topic":          func(c *Config) { c.Topic = "Az09._-" + strings.Repeat("t", maxTopicLength-7) },
		"smallest record limit":  func(c *Config) { c.MaxMessageBytes = 512 },
		"largest record limit":   func(c *Config) { c.MaxMessageBytes = 1 << 30 },
		"shortest send timeout":  func(c *Config) { c.SendTimeout = units.Duration(2 * time.Second) },
		"SASL over TLS":          withSASL,
		"plaintext without SASL": func(c *Config) { c.TLS.Insecure = true },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			fn(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Errorf("the config was rejected, want it accepted: %v", err)
			}
		})
	}
}

func TestConfigRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"no brokers":                   func(c *Config) { c.Brokers = nil },
		"empty broker list":            func(c *Config) { c.Brokers = []string{} },
		"broker without a port":        func(c *Config) { c.Brokers = []string{"kafka-0"} },
		"broker with a scheme":         func(c *Config) { c.Brokers = []string{"kafka://kafka-0:9092"} },
		"broker port above the range":  func(c *Config) { c.Brokers = []string{"kafka-0:65536"} },
		"no topic":                     func(c *Config) { c.Topic = "" },
		"dot topic":                    func(c *Config) { c.Topic = "." },
		"double dot topic":             func(c *Config) { c.Topic = ".." },
		"topic length above the limit": func(c *Config) { c.Topic = strings.Repeat("t", maxTopicLength+1) },
		"topic with a slash":           func(c *Config) { c.Topic = "team/events" },
		"unknown message key":          func(c *Config) { c.MessageKey = "pod" },
		"unknown required acks":        func(c *Config) { c.RequiredAcks = "-1" },
		"unknown compression":          func(c *Config) { c.Compression = "brotli" },
		"no record limit":              func(c *Config) { c.MaxMessageBytes = 0 },
		"record limit below the range": func(c *Config) { c.MaxMessageBytes = 511 },
		"record limit above the range": func(c *Config) { c.MaxMessageBytes = 1<<30 + 1 },
		"send timeout below the minimum": func(c *Config) {
			c.SendTimeout = units.Duration(2*time.Second - time.Millisecond)
		},
		"unknown SASL mechanism": func(c *Config) {
			withSASL(c)
			c.Auth.SASL.Mechanism = "GSSAPI"
		},
		"SASL without a username": func(c *Config) {
			withSASL(c)
			c.Auth.SASL.Username = ""
		},
		"SASL password over plaintext": func(c *Config) {
			withSASL(c)
			c.TLS.Insecure = true
		},
		"invalid TLS block":   func(c *Config) { c.TLS.CAFile, c.TLS.CAPEM = "/ca.crt", "-----BEGIN" },
		"invalid queue block": func(c *Config) { c.Queue.MaxBytes = sink.QueueChunkSize - 1 },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			fn(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("the config was accepted, want it rejected")
			}
		})
	}
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Brokers = []string{"kafka-0:9092"}
	cfg.Topic = "events"

	return cfg
}

func withSASL(c *Config) {
	c.Auth.SASL = &SASLConfig{
		Mechanism: SASLMechanismSCRAMSHA512,
		Username:  "agent",
		Password:  "secret",
	}
}
