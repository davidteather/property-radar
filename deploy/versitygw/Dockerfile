# Wraps the VersityGW image with an entrypoint that creates the object + sidecar
# directories a fresh Railway volume lacks, then starts the gateway (docs/decisions.md).
FROM versity/versitygw:v1.7.0
COPY deploy/versitygw/entrypoint.sh /vgw-entrypoint.sh
ENTRYPOINT ["/bin/sh", "/vgw-entrypoint.sh"]
