.PHONY: build web test run demo image helm-check compatibility

web:
	cd web && npm ci && npm run build
	find internal/server/ui -mindepth 1 -maxdepth 1 ! -name placeholder.txt -exec rm -rf {} +
	cp -R web/dist/. internal/server/ui/

build: web
	mkdir -p .build
	CGO_ENABLED=0 go build -trimpath -o .build/alertmanager-ui ./cmd/alertmanager-ui

test:
	go test -race ./...
	cd web && npm ci && npm test && npm run build

run: build
	.build/alertmanager-ui

demo: build
	DEMO=true .build/alertmanager-ui

image:
	docker build -t aai-alertmanager-ui:local .

helm-check:
	helm lint charts/aai-alertmanager-ui --set alertmanager.url=http://alertmanager:9093
	helm template alerts charts/aai-alertmanager-ui --set alertmanager.url=http://alertmanager:9093 > /dev/null

compatibility: build
	python3 scripts/compatibility.py --binary .build/alertmanager-ui --versions 0.27.0 0.34.1
