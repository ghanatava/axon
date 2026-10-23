# Build artifacts first with scripts/build-image.sh; CO-RE relocates on the node.
FROM debian:bookworm-slim
COPY build/axon-agent /usr/local/bin/axon-agent
COPY build/tls_capture.bpf.o /opt/axon/tls_capture.bpf.o
USER 0
EXPOSE 9108
ENTRYPOINT ["/usr/local/bin/axon-agent"]
CMD ["-discover", "-bpf", "/opt/axon/tls_capture.bpf.o", "-metrics-listen", ":9108"]
