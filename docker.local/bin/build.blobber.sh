#!/bin/sh
set -e

GIT_COMMIT=$(git rev-list -1 HEAD)
echo $GIT_COMMIT

TOP="$PWD"
if [ ! -d ./code/go/0chain.net/gosdk ]; then
  git clone git@github.com:0chain/gosdk.git ./code/go/0chain.net/gosdk
  cd ./code/go/0chain.net/gosdk
  git checkout jssdk
  cd $TOP
fi

docker build --build-arg GIT_COMMIT=$GIT_COMMIT -f docker.local/ValidatorDockerfile . -t validator
docker build --build-arg GIT_COMMIT=$GIT_COMMIT -f docker.local/Dockerfile . -t blobber

for i in $(seq 1 6);
do
  BLOBBER=$i docker-compose -p blobber$i -f docker.local/docker-compose.yml build --force-rm
done

docker.local/bin/sync_clock.sh
