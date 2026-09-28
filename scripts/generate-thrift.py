#!/usr/bin/env python3
"""Project the five approved methods from the unchanged, checksum-pinned IDL."""
import argparse
import hashlib
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'third_party/hive-thrift/hive_metastore.thrift'
SHA256 = 'b4a4be314077eeafe487d2c004d7dcea949141a2f716dd86ff01c00e58651575'
METHODS = ('get_all_databases', 'get_database', 'get_table_req', 'get_table', 'get_table_meta')
VERSION = '0.24.0'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    raw = SOURCE.read_bytes()
    if hashlib.sha256(raw).hexdigest() != SHA256:
        raise SystemExit('Upstream IDL checksum mismatch')
    source = raw.decode()
    # The checksum pins the grammar handled here; fail on any missing declaration.
    clean = re.sub(r'/\*.*?\*/|//[^\n]*', '', source, flags=re.S)
    declarations = dict((m[2], m[0]) for m in re.finditer(
        r'\b(struct|enum|exception)\s+(\w+)\s*\{[^}]*\}', clean))
    # Go cannot represent Hive's map<list<string>, string> skew-location keys.
    # This optional descriptor is never emitted by the external-Delta contract.
    declarations["StorageDescriptor"], count = re.subn(
        r"(?m)^\s*11: optional SkewedInfo skewedInfo,?[^\n]*",
        "", declarations["StorageDescriptor"])
    if count != 1:
        raise SystemExit("Expected optional StorageDescriptor.skewedInfo field")
    methods = []
    for name in METHODS:
        match = re.search(r'(?m)^\s*[^\n]+\b' + name + r'\([^)]*\)\s*throws\s*\([^)]*\)', clean)
        if not match:
            raise SystemExit('Missing method: ' + name)
        methods.append(match[0].strip())
    needed = set()
    pending = '\n'.join(methods)
    while True:
        found = set(re.findall(r'\b\w+\b', pending)) & declarations.keys() - needed
        if not found:
            break
        needed.update(found)
        pending = '\n'.join(declarations[name] for name in sorted(found))
    header = source[:source.index('namespace')]
    projection = header + '// Generated projection; run scripts/generate-thrift.py.\nnamespace go hms\n\n'
    projection += '\n\n'.join(body for name, body in declarations.items() if name in needed)
    projection += '\n\nservice ThriftHiveMetastore {\n  ' + '\n  '.join(methods) + '\n}\n'
    version = subprocess.check_output(['thrift', '-version'], text=True).strip()
    if version != 'Thrift version ' + VERSION:
        raise SystemExit('Required Thrift compiler ' + VERSION + ', got ' + version)
    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp)
        idl = path / 'hms.thrift'
        idl.write_text(projection)
        subprocess.run(['thrift', '--gen', 'go:skip_remote', '-out', tmp, str(idl)], check=True)
        files = {ROOT / 'internal/thrift/idl/hms.thrift': projection.encode()}
        for generated in (path / 'hms').glob('*.go'):
            subprocess.run(['gofmt', '-w', str(generated)], check=True)
            files[ROOT / 'internal/thrift/generated' / generated.name] = generated.read_bytes()
        if not any(p.suffix == '.go' for p in files):
            raise SystemExit('Compiler produced no Go bindings')
        for target, content in files.items():
            if args.check:
                if not target.exists() or target.read_bytes() != content:
                    raise SystemExit('Generated file differs: ' + str(target.relative_to(ROOT)))
            else:
                target.write_bytes(content)
    print('Thrift bindings ' + ('verified' if args.check else 'generated'))


if __name__ == '__main__':
    main()
