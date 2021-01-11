#!/bin/sh
img=cluster-operator:v1
docker rmi $img
make docker-build deploy IMG=$img
