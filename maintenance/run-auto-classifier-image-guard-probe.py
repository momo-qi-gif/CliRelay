#!/usr/bin/env python3
"""Disposable authenticated/TLS binary test, isolated from the internet."""
from pathlib import Path
import argparse
import datetime
import hashlib
import ipaddress
import json
import os
import secrets
import subprocess
import time
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID

ROOT = Path(__file__).resolve().parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('binary', type=Path, help='Local binary to test; no upload or deployment')
parser.add_argument('--mode', choices=('baseline', 'guarded'), default='guarded')
parser.add_argument('--output-dir', type=Path, required=True, help='Local directory for logs and temporary test certificates')
parser.add_argument('--tools-image', default='clirelay-reviewed-testtools:20261104', help='Existing local image with Python 3 and CA roots; never pulled')
args = parser.parse_args()
BINARY = args.binary.resolve(strict=True)
ARTIFACT = BINARY.parent
BINARY_NAME = BINARY.name
MODE = args.mode
EXPECTED = hashlib.sha256(BINARY.read_bytes()).hexdigest()
suffix = secrets.token_hex(4)
network = 'cgy-bridge-http-'+suffix
pg = 'cgy-bridge-pg-'+suffix
probe = 'cgy-bridge-probe-'+suffix
output_root = args.output_dir.resolve()
output_root.mkdir(mode=0o700, parents=True, exist_ok=True)
out = output_root/('http-'+MODE+'-'+suffix)
out.mkdir(mode=0o700)
certs = out/'certs'; certs.mkdir(mode=0o755)
work = out/'work'; work.mkdir(mode=0o700)
now = datetime.datetime.now(datetime.timezone.utc)
ca_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
ca_name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, 'Disposable classifier test CA')])
ca = (x509.CertificateBuilder().subject_name(ca_name).issuer_name(ca_name)
      .public_key(ca_key.public_key()).serial_number(x509.random_serial_number())
      .not_valid_before(now-datetime.timedelta(minutes=1)).not_valid_after(now+datetime.timedelta(hours=2))
      .add_extension(x509.BasicConstraints(ca=True, path_length=0), critical=True)
      .add_extension(x509.KeyUsage(digital_signature=True, content_commitment=False, key_encipherment=False,
                     data_encipherment=False, key_agreement=False, key_cert_sign=True, crl_sign=True,
                     encipher_only=False, decipher_only=False), critical=True)
      .add_extension(x509.SubjectKeyIdentifier.from_public_key(ca_key.public_key()), critical=False)
      .sign(ca_key, hashes.SHA256()))
(certs/'ca.crt').write_bytes(ca.public_bytes(serialization.Encoding.PEM))
for name, san in [('pg', x509.DNSName('pg')), ('loopback', x509.IPAddress(ipaddress.ip_address('127.0.0.1')))]:
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    cert = (x509.CertificateBuilder().subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, name)]))
            .issuer_name(ca_name).public_key(key.public_key()).serial_number(x509.random_serial_number())
            .not_valid_before(now-datetime.timedelta(minutes=1)).not_valid_after(now+datetime.timedelta(hours=2))
            .add_extension(x509.SubjectAlternativeName([san]), critical=False)
            .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
            .add_extension(x509.KeyUsage(digital_signature=True, content_commitment=False, key_encipherment=True,
                           data_encipherment=False, key_agreement=False, key_cert_sign=False, crl_sign=False,
                           encipher_only=False, decipher_only=False), critical=True)
            .add_extension(x509.SubjectKeyIdentifier.from_public_key(key.public_key()), critical=False)
            .add_extension(x509.AuthorityKeyIdentifier.from_issuer_public_key(ca_key.public_key()), critical=False)
            .sign(ca_key, hashes.SHA256()))
    (certs/(name+'.crt')).write_bytes(cert.public_bytes(serialization.Encoding.PEM))
    path = certs/(name+'.key')
    path.write_bytes(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.TraditionalOpenSSL, serialization.NoEncryption()))
    path.chmod(0o600)

def docker(*args, check=True, timeout=100):
    run = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=timeout)
    if check and run.returncode:
        raise RuntimeError(run.stderr[:1200])
    return run

exit_code = 1
try:
    docker('network', 'create', '--internal', network)
    start = ('cp /certs/pg.key /tmp/cgy-pg.key && chmod 600 /tmp/cgy-pg.key && chown postgres:postgres /tmp/cgy-pg.key && '
             'exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/certs/pg.crt -c ssl_key_file=/tmp/cgy-pg.key')
    docker('run', '-d', '--pull', 'never', '--name', pg, '--network', network, '--network-alias', 'pg',
           '--tmpfs', '/var/lib/postgresql/data:rw', '-v', str(certs)+':/certs:ro',
           '-e', 'POSTGRES_USER=localprobe', '-e', 'POSTGRES_PASSWORD=synthetic-only', '-e', 'POSTGRES_DB=localprobe',
           '--entrypoint', 'sh', 'postgres:16-alpine', '-c', start)
    deadline = time.monotonic()+30
    while docker('exec', pg, 'pg_isready', '-h', '127.0.0.1', '-U', 'localprobe', '-d', 'localprobe', check=False).returncode:
        if time.monotonic() > deadline: raise RuntimeError('Temporary PostgreSQL unavailable')
        time.sleep(.2)
    run = docker('run', '--rm', '--pull', 'never', '--name', probe, '--network', network,
                 '--user', f'{os.getuid()}:{os.getgid()}', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                 '-v', str(ARTIFACT)+':/artifact:ro', '-v', str(work)+':/work', '-v', str(certs)+':/certs:ro',
                 '-v', str(ROOT/'verify-auto-classifier-image-guard.py')+':/verify.py:ro', '--entrypoint', 'python3',
                 args.tools_image, '-I', '/verify.py', BINARY_NAME, EXPECTED, MODE, check=False, timeout=240)
    (out/'runner.log').write_text(run.stdout+run.stderr)
    print(run.stdout+run.stderr)
    exit_code = run.returncode
finally:
    docker('rm', '-f', probe, check=False)
    docker('rm', '-f', pg, check=False)
    docker('network', 'rm', network, check=False)
    cleanup = {'binary_sha256': EXPECTED, 'probe_exit_code': exit_code, 'artifact_directory': str(out),
               'production_used': False, 'network_internal': True, 'tls_verification_configured': True,
               'tls_probe_passed': exit_code == 0,
               'probe_absent': docker('container', 'inspect', probe, check=False).returncode != 0,
               'postgres_absent': docker('container', 'inspect', pg, check=False).returncode != 0,
               'network_absent': docker('network', 'inspect', network, check=False).returncode != 0}
    (out/'cleanup.json').write_text(json.dumps(cleanup, indent=2))
    print(json.dumps(cleanup))
raise SystemExit(exit_code)
