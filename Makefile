.PHONY: config-schema

# Regenerates config/schema/agent.config.schema.json from the config types.
config-schema:
	go generate ./config
