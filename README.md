**Currently in a rough working state. If I find the time I might improve it.**

`go-docker-melt` is a simple tool to merge layers of Docker images. It takes
a tar file produced by `docker save` as input and produces a tar file that can
be imported with `docker load`.

When the input tar file to `go-docker-melt` only contains a single Docker image
`go-docker-melt` will melt all layers into a single layer. If the input tar
file contains multiple Docker images `go-docker-melt` will minimize the number
of layers. It will melt sequences of shared layers between images and sequences
of unique layers but will not melt unique layers into shared layers.

Note that `go-docker-melt` is only intended to work with images relying on the
`manifest.json` file. As does the Docker daemon in newer versions,
`go-docker-melt` ignores per layer configuration files.

Usage is pretty simple:

```
go-docker-melt -i input.tar -o output.tar -t tmpdir
```

Note that in order to preserve all permissions etc. `go-docker-melt` should be run as
root. The resulting image can then be imported via:

```
docker load -i output.tar
```
