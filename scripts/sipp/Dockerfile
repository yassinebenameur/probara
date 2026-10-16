# SIPp UAS for worker/internal/worker/sip_invite_integration_test.go.
#
# The base image is pinned by digest and the package by version so the test
# runs the same SIPp everywhere; bump both together. SIPp (Debian's
# sip-tester) is a third-party SIP implementation the hand-rolled INVITE
# client is checked against, next to the scripted fake UAS in
# sip_invite_test.go. The scenarios live in the test's testdata/sipp/ and are
# copied in at container start, so editing one needs no rebuild.
FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587

RUN apt-get update \
 && apt-get install -y --no-install-recommends sip-tester=1:3.6.1-1+b2 \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /sipp
EXPOSE 5060/udp 5060/tcp
ENTRYPOINT ["sipp"]
