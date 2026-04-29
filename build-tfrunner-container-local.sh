#!/bin/bash
mkdir -p /tmp/tfrunner-build \
&& docker build . -f Dockerfile-build -t tfrunner-build \
&& docker run -v /tmp/tfrunner-build:/tmp tfrunner-build \
&& cp /tmp/tfrunner-build/tfrunner . \
&& docker build . -f Dockerfile-local -t tfrunner \
&& rm ./tfrunner