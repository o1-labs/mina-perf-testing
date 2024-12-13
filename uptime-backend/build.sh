#!/usr/bin/env bash
set -e

if [[ "$OUT" == "" ]]; then
  OUT=${PWD}/result
fi

function build_c_reference_signer {
  mkdir -p ${OUT}/bin
  rm -Rf ${OUT}/c-reference-signer ${OUT}/headers ${OUT}/libmina_signer.so # Otherwise re-building without clean causes permissions issue
  if [[ "$PKG_MINA_SIGNER" == "" ]]; then
    # No nix
    git clone -b v1.0.0 --depth 1 https://github.com/MinaProtocol/c-reference-signer.git ${OUT}/c-reference-signer
    cp -f ./Makefile-c-ref-signer ${OUT}/c-reference-signer/Makefile
    make -C ${OUT}/c-reference-signer clean libmina_signer.so
    cp ${OUT}/c-reference-signer/libmina_signer.so ${OUT}
    mkdir -p ${OUT}/headers
    cp ${OUT}/c-reference-signer/*.h ${OUT}/headers
  else
    ln -s ${PKG_MINA_SIGNER}/lib/libmina_signer.so ${OUT}/libmina_signer.so
    ln -s ${PKG_MINA_SIGNER}/headers ${OUT}/headers
  fi
}

case "${1}" in
test)
  build_c_reference_signer
  cd src/uptime_backend
  LD_LIBRARY_PATH=${OUT} ${GO} test
  cd ../..
  ;;
docker-publish)
  DOCKER_IMAGE_TAG=in-memory-uptime-backend
  docker build -t ${DOCKER_IMAGE_TAG} .
  docker tag ${DOCKER_IMAGE_TAG} o1labs/mina-perf-testing:${DOCKER_IMAGE_TAG}
  docker push o1labs/mina-perf-testing:${DOCKER_IMAGE_TAG}
  ;;
"")
  build_c_reference_signer
  cd src/cmd/uptime_backend
  ${GO} build -o ${OUT}/bin/uptime_backend
  echo ""
  echo "To run the application please use the following command: LD_LIBRARY_PATH=result ./result/bin/uptime_backend"
  ;;
*)
  echo "Unknown command ${1}"
  exit 2
  ;;
esac
