all:
	docker buildx build -t mkevac/markocaloriesbot --load .

push:
	docker buildx build --platform linux/amd64,linux/arm64 -t mkevac/markocaloriesbot --push .

run:
	docker-compose up -d

stop:
	docker-compose down
