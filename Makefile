# make build — бинарь ./matterqute с версией из git describe (в «О программе»).
# make release — следующий тег vX.Y.Z и push ветки с тегом: GitHub Actions
# соберёт сборки и создаст релиз (.github/workflows/release.yml, заметки —
# из заголовков коммитов). По умолчанию растёт патч; BUMP=minor или
# BUMP=major — минорная или мажорная; TAG=v1.2.3 — свой тег; YES=1 — без
# вопроса. Нужны чистое дерево и ветка main.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS := -X matterqute/internal/ui.Version=$(VERSION)

BUMP ?= patch
LAST := $(or $(shell git tag --list 'v*' --sort=-v:refname | head -n 1),v0.0.0)
ifndef TAG
TAG := $(shell echo '$(LAST)' | awk -F. -v b='$(BUMP)' '{ sub(/^v/, "", $$1); \
	if (b == "major") { $$1++; $$2 = 0; $$3 = 0 } \
	else if (b == "minor") { $$2++; $$3 = 0 } \
	else if (b == "patch") { $$3++ } \
	else { print "BUMP=" b ": ожидается patch, minor или major" > "/dev/stderr"; exit 1 } \
	printf "v%d.%d.%d", $$1, $$2, $$3 }')
endif

.PHONY: build test release

build:
	go build -ldflags "$(LDFLAGS)" -o matterqute ./cmd/matterqute

test:
	go test ./...

release:
	@test -n '$(TAG)' || { echo 'не вышло посчитать тег после $(LAST)'; exit 1; }
	@test "$$(git rev-parse --abbrev-ref HEAD)" = main || { echo "релиз только с main, сейчас $$(git rev-parse --abbrev-ref HEAD)"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo 'дерево не чистое:'; git status --short; exit 1; }
	@! git rev-parse -q --verify 'refs/tags/$(TAG)' >/dev/null || { echo 'тег $(TAG) уже есть'; exit 1; }
	@test -n "$$(git log --oneline '$(LAST)..HEAD' 2>/dev/null)" || { echo 'после $(LAST) нет новых коммитов'; exit 1; }
	@echo 'Релиз $(TAG) (после $(LAST)):'; git log --oneline '$(LAST)..HEAD'
	@test -n '$(YES)' || { printf 'Поставить тег $(TAG) и запушить? [y/N] '; read -r a; test "$$a" = y || { echo отмена; exit 1; }; }
	git tag -a '$(TAG)' -m '$(TAG)'
	git push origin HEAD '$(TAG)'
	@echo 'Сборка и релиз: https://github.com/dmitry-novozhilov/matterqute/actions'
