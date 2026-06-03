.PHONY: phase0-up phase0-down phase0-logs agent run-agent tidy

phase0-up:
	cd deploy/phase0 && docker compose up -d

phase0-down:
	cd deploy/phase0 && docker compose down

phase0-logs:
	cd deploy/phase0 && docker compose logs -f

agent:
	go run ./cmd/agent

tidy:
	go mod tidy

run-agent: phase0-up
	go run ./cmd/agent
