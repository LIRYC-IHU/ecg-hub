# ─────────────────────────────────────────────────────────────────────────────
# ECG Hub — Makefile
#
#   make            → build everything (backend + frontend) locally
#   make init       → create .env and config.yaml from the example files
#   make docker     → build images and start the prod stack (detached)
#   make dev        → start the local dev stack (air + vite hot-reload)
#   make clean      → remove build artefacts and stop containers
#   make fclean     → clean + drop docker volumes/images and node_modules
#   make re         → fclean then rebuild
# ─────────────────────────────────────────────────────────────────────────────

# ── Config ───────────────────────────────────────────────────────────────────
BACKEND_DIR   := backend
FRONTEND_DIR  := frontend
BIN           := $(BACKEND_DIR)/ecg-hub
CMD           := ./cmd/ecg-hub
# Research = the curated API-key surface for external researchers (webhook → pull).
# Only handlers annotated with this tag (all carry @Security ApiKeyAuth) are documented.

COMPOSE       := docker compose
COMPOSE_DEV   := docker compose -f docker-compose.dev.yml

# ── Pretty print ─────────────────────────────────────────────────────────────
CYAN  := \033[36m
GREEN := \033[32m
RESET := \033[0m
define log
	@printf "$(CYAN)▶ %s$(RESET)\n" "$(1)"
endef

.DEFAULT_GOAL := all
.PHONY: all help init build build-backend build-frontend \
        docker up down dev logs clean fclean re

# ── Build ────────────────────────────────────────────────────────────────────
all: build ## Build backend + frontend

build: build-backend build-frontend ## Compile both apps locally

build-backend: ## Compile the Go backend binary
	$(call log,Building backend → $(BIN))
	@cd $(BACKEND_DIR) && go build -ldflags="-s -w" -o ecg-hub $(CMD)
	@printf "$(GREEN)✓ backend built$(RESET)\n"

build-frontend: $(FRONTEND_DIR)/node_modules ## Build the React frontend (tsc + vite)
	$(call log,Building frontend)
	@cd $(FRONTEND_DIR) && yarn build
	@printf "$(GREEN)✓ frontend built$(RESET)\n"

$(FRONTEND_DIR)/node_modules: $(FRONTEND_DIR)/package.json
	$(call log,Installing frontend dependencies)
	@cd $(FRONTEND_DIR) && yarn install --frozen-lockfile
	@touch $@

# ── Init (.env + config) ─────────────────────────────────────────────────────
init: .env config.yaml ## Create .env and config.yaml from the example files

.env:
	$(call log,Creating .env from .env.example)
	@cp .env.example .env
	@printf "$(GREEN)✓ .env created — edit your secrets$(RESET)\n"

config.yaml:
	$(call log,Creating config.yaml from config.example.yaml)
	@cp config.example.yaml config.yaml
	@printf "$(GREEN)✓ config.yaml created$(RESET)\n"

# ── Docker ───────────────────────────────────────────────────────────────────
docker: up ## Alias for `up`

up: init ## Build images and start the prod stack (detached)
	$(call log,Starting prod stack)
	@$(COMPOSE) up -d --build

down: ## Stop the prod stack
	$(call log,Stopping prod stack)
	@$(COMPOSE) down

dev: init ## Start the local dev stack (hot-reload)
	$(call log,Starting dev stack)
	@$(COMPOSE_DEV) up --build

logs: ## Follow prod stack logs
	@$(COMPOSE) logs -f

# ── Clean ────────────────────────────────────────────────────────────────────
clean: ## Remove build artefacts and stop containers
	$(call log,Cleaning build artefacts)
	@rm -f $(BIN) $(BACKEND_DIR)/main
	@rm -rf $(FRONTEND_DIR)/dist
	@-$(COMPOSE) down 2>/dev/null || true
	@cd $(BACKEND_DIR) && go clean
	@printf "$(GREEN)✓ cleaned$(RESET)\n"

fclean: clean ## clean + drop docker volumes/images and node_modules
	$(call log,Full clean — volumes, images, node_modules)
	@-$(COMPOSE) down -v --rmi local 2>/dev/null || true
	@-$(COMPOSE_DEV) down -v --rmi local 2>/dev/null || true
	@rm -rf $(FRONTEND_DIR)/node_modules
	@printf "$(GREEN)✓ full clean done (.env and config.yaml kept)$(RESET)\n"

re: fclean all ## Rebuild from scratch

# ── Help ─────────────────────────────────────────────────────────────────────
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-16s$(RESET) %s\n", $$1, $$2}'
