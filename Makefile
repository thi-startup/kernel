APP = spitfire-build-kernel
MAIN = $(addprefix cmd/,$(addsuffix /main.go, $(APP)))
BIN = $(addprefix bin/, $(APP))
ARCH = $(shell uname -m)

.PHONY: all build-image run-container

all: build-image run-container

$(BIN): $(MAIN)
	CGO_ENABLED=0 go build -o $@ $<

build-image: $(BIN)
	$(BIN) -arch $(ARCH) -$@

run-container: build-image
	$(BIN) -arch $(ARCH) -$@

clean:
	rm -f $(BIN)
