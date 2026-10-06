.PHONY: all push run stop build test bump version

all:
	just docker

push:
	just push

run:
	just run

stop:
	just stop

build test bump version:
	just $@
