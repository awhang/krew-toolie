# Makefile for krew-toolie
#
# Wraps the repo's docker compose so you never have to remember the --build
# flag: `make up` always rebuilds the image from the Dockerfile before starting.
#
# Note: plain `docker compose up` does NOT rebuild by default (it reuses the
# previous image), which is why these targets pass --build explicitly.

COMPOSE := docker compose

.PHONY: help up down restart logs build ps

## Show targets with descriptions
help: ## Show this help
	@egrep -h '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

up: ## Build the image and start all services in the background (always rebuilds)
	$(COMPOSE) up -d --build

down: ## Stop all services (preserves the Postgres data volume)
	$(COMPOSE) down

restart: ## Rebuild and restart the app service (fast iteration on code changes)
	$(COMPOSE) up -d --build app

logs: ## Stream the app logs (Ctrl-C to stop)
	$(COMPOSE) logs -f app

build: ## Build the bot image without starting services
	$(COMPOSE) build

ps: ## List running services
	$(COMPOSE) ps
