#!/bin/sh
img=tmaxcloudck/cluster-manager-operator:b5.0.2.5
docker rmi $img
make docker-build docker-push IMG=$img
