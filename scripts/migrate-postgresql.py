"""One-time legacy reader. Not included in the console runtime image.

Credentials come from a mode-0600 JSON file, never command-line arguments.
The default inspect operation is read-only. No legacy object is modified/deleted.
"""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import sys
from concurrent.futures import ThreadPoolExecutor
from collections import Counter

import boto3
from botocore.config import Config


def legacy_client(env):
    return boto3.client(
        "s3", endpoint_url=env.get("STATE_S3_ENDPOINT") or env["S3_ENDPOINT"],
        aws_access_key_id=env["STATE_AWS_ACCESS_KEY_ID"],
        aws_secret_access_key=env["STATE_AWS_SECRET_ACCESS_KEY"], region_name="auto",
        config=Config(connect_timeout=10, read_timeout=120, retries={"max_attempts": 3}),
    )


def price_document(client, env):
    key = env.get("STATE_S3_PREFIX", "analytics/v1").strip("/") + "/prices.json"
    obj = client.get_object(Bucket=env["STATE_S3_BUCKET"], Key=key)
    raw = obj["Body"].read(32 * 1024 * 1024 + 1)
    if len(raw) > 32 * 1024 * 1024:
        raise ValueError("price document exceeds migration bound")
    doc = json.loads(raw)
    if doc.get("schema") != 1 or not isinstance(doc.get("prices"), list):
        raise ValueError("unsupported price document")
    names = [p["model"] for p in doc["prices"]]
    if len(set(names)) != len(names):
        raise ValueError("duplicate model prices")
    return doc, obj["ETag"], hashlib.sha256(raw).hexdigest()


def checkpoint_key(env):
    identity = "\0".join([
        env["S3_ENDPOINT"], env["S3_BUCKET"], env.get("S3_PREFIX", "uni-api-facts/v1/"),
        env.get("ANALYTICS_TIMEZONE", "Asia/Shanghai"),
    ])
    digest = hashlib.sha256(identity.encode()).hexdigest()
    return env.get("STATE_S3_PREFIX", "analytics/v1").strip("/") + "/cache-v5-" + digest + ".duckdb.gz"


def inspect(client, env):
    doc, _, digest = price_document(client, env)
    obj = client.head_object(Bucket=env["STATE_S3_BUCKET"], Key=checkpoint_key(env))
    manifest = json.loads(obj["Metadata"]["manifest"])
    print(json.dumps({"operation": "inspect", "price_models": len(doc["prices"]),
                      "prices_sha256": digest, "checkpoint_bytes": manifest["bytes"],
                      "compressed_bytes": obj["ContentLength"], "rows": manifest["rows"],
                      "database_version": manifest["version"],"modified_at":obj["LastModified"].isoformat(),"compressed_sha256":obj["Metadata"]["sha256"]}))


def download(client, env, destination):
    # Immutable read: bind the download to one ETag before validating both hashes.
    key = checkpoint_key(env)
    meta = client.head_object(Bucket=env["STATE_S3_BUCKET"], Key=key)
    manifest = json.loads(meta["Metadata"]["manifest"])
    obj = client.get_object(Bucket=env["STATE_S3_BUCKET"], Key=key, IfMatch=meta["ETag"])
    destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    compressed = destination.with_suffix(".pending.gz")
    digest = hashlib.sha256()
    with os.fdopen(os.open(compressed, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as out:
        while part := obj["Body"].read(1024 * 1024):
            digest.update(part)
            out.write(part)
    if digest.hexdigest() != meta["Metadata"]["sha256"]:
        raise ValueError("compressed checksum mismatch")
    digest = hashlib.sha256()
    total = 0
    with gzip.open(compressed, "rb") as source, os.fdopen(os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as out:
        while part := source.read(1024 * 1024):
            total += len(part)
            if total > manifest["bytes"]:
                raise ValueError("uncompressed size mismatch")
            digest.update(part)
            out.write(part)
    if total != manifest["bytes"] or digest.hexdigest() != manifest["sha256"]:
        raise ValueError("uncompressed checksum mismatch")
    destination.with_suffix(".manifest.json").write_text(json.dumps(manifest))
    print(json.dumps({"operation": "download", "verified": True, "bytes": total, "sha256": digest.hexdigest()}))


def download_recent(env, journal, destination, hours):
    records = [json.loads(line) for line in journal.open()]
    newest = max(row[4] for row in records)
    records = sorted((row for row in records if row[4] >= newest - hours * 3600000), key=lambda row: (-row[4], -row[0]))
    client = boto3.client("s3", endpoint_url=env["S3_ENDPOINT"],
                          aws_access_key_id=env["AWS_ACCESS_KEY_ID"], aws_secret_access_key=env["AWS_SECRET_ACCESS_KEY"],
                          region_name="auto", config=Config(connect_timeout=10, read_timeout=30, max_pool_connections=8, retries={"max_attempts": 3}))
    destination.mkdir(mode=0o700, parents=True, exist_ok=True)

    def fetch(record):
        seq, key, etag, expected_bytes, modified = record
        digest = key.rsplit("/batch-", 1)[-1].removesuffix(".jsonl")
        if len(digest) != 64 or any(c not in "0123456789abcdef" for c in digest):
            raise ValueError("unsupported immutable object identity")
        path = destination / (digest + ".jsonl")
        if path.exists():
            raw = path.read_bytes()
        else:
            obj = client.get_object(Bucket=env["S3_BUCKET"], Key=key, IfMatch=etag)
            raw = obj["Body"].read(32 * 1024 * 1024 + 1)
        if len(raw) != expected_bytes or hashlib.sha256(raw).hexdigest() != digest:
            raise ValueError("immutable object verification failed")
        if not path.exists():
            with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as out:
                out.write(raw)
        return [seq, digest, expected_bytes, modified]

    # Consume small chunks to keep concurrent requests and metadata memory bounded.
    done = 0
    with ThreadPoolExecutor(max_workers=8) as pool, (destination / "manifest.jsonl").open("w") as manifest:
        for start in range(0, len(records), 64):
            for result in pool.map(fetch, records[start:start + 64]):
                manifest.write(json.dumps(result) + "\n")
                done += 1
            manifest.flush()
            if done % 1024 == 0:
                print(json.dumps({"operation": "recent-download", "objects_verified": done, "total_objects": len(records)}), flush=True)
    print(json.dumps({"operation": "recent-download", "verified": True, "objects": done, "hours": hours, "through_modified_ms": newest}), flush=True)


def prepare_facts(inputs, destination, source_id):
    """Verify every archived batch again and write bounded hourly import files."""
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    seen, counts, hours = set(), Counter(), {}
    for directory in inputs:
        with (directory / "manifest.jsonl").open() as manifest:
            for line in manifest:
                _, digest, expected_bytes, _ = json.loads(line)
                if digest in seen:
                    continue
                raw = (directory / (digest + ".jsonl")).read_bytes()
                if len(raw) != expected_bytes or hashlib.sha256(raw).hexdigest() != digest:
                    raise ValueError("immutable object verification failed")
                seen.add(digest)
                for record in raw.splitlines():
                    if not record.strip():
                        continue
                    fact = json.loads(record)
                    if fact.get("schema") != 1 or not isinstance(fact.get("at_ms"), int):
                        raise ValueError("invalid legacy fact")
                    fact["source_id"] = source_id
                    fact["event_id"] = source_id + "::" + fact["event_id"]
                    hour = fact["at_ms"] // 3600000 * 3600000
                    if hour not in hours:
                        path = destination / (str(hour) + ".jsonl")
                        hours[hour] = os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w")
                    hours[hour].write(json.dumps(fact, separators=(",", ":")) + "\n")
                    counts[fact["kind"]] += 1
    for file in hours.values():
        file.close()
    summary = {"operation": "prepare-facts", "verified": True, "objects": len(seen),
               "hours": len(hours), "events_by_kind": dict(counts)}
    (destination / "summary.json").write_text(json.dumps(summary))
    print(json.dumps(summary), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=["inspect", "download", "recent-download", "prepare-facts"])
    parser.add_argument("--credentials", type=Path)
    parser.add_argument("--destination", type=Path)
    parser.add_argument("--journal", type=Path)
    parser.add_argument("--hours", type=int, default=24)
    parser.add_argument("--input", action="append", type=Path)
    parser.add_argument("--source-id", default="primary")
    args = parser.parse_args()
    if args.operation == "prepare-facts":
        if not args.input or not args.destination or not args.source_id:
            raise ValueError("prepare-facts requires inputs, destination and source identity")
        prepare_facts(args.input, args.destination, args.source_id)
        return
    if not args.credentials:
        raise ValueError("legacy reader requires credentials")
    if args.credentials.stat().st_mode & 0o077:
        raise ValueError("credential file must be private")
    env = json.loads(args.credentials.read_text())
    if args.operation == "recent-download":
        if not args.destination or not args.journal or args.hours <= 0:
            raise ValueError("recent download requires journal, destination and positive hours")
        download_recent(env, args.journal, args.destination, args.hours)
        return
    client = legacy_client(env)
    if args.operation == "inspect":
        inspect(client, env)
    else:
        if not args.destination:
            raise ValueError("download destination is required")
        download(client, env, args.destination)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # SDK exception strings can contain endpoint or credential material.
        print(json.dumps({"status": "failed", "error_type": type(error).__name__}), file=sys.stderr)
        raise SystemExit(1)
