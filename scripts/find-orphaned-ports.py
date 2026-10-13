#!/usr/bin/env python3
"""Find (and optionally delete) orphaned Neutron ports on PowerVC/OpenStack.

Only ports in the current project are considered, even with admin
credentials, so that ports are compared against the servers of the same
project.

A port is reported as orphaned when either:

  * unattached: it has no device_id (nothing is using it), or
  * stale:      its device_owner is "compute:*" but its device_id refers to a
                server that no longer exists (confirmed by a direct lookup).

Never reported:

  * ports owned by anything other than compute (network:*, Octavia, trunk
    subports, bare metal, ...)
  * ports whose fixed IPs are referenced by any visible port's
    allowed_address_pairs (e.g. the OpenShift API and Ingress VIP ports)

Credentials come from clouds.yaml via --cloud or the OS_CLOUD environment
variable; nothing is hard-coded.

By default the script only reports. Pass --delete to remove the reported
ports; you will be asked to confirm unless --yes is also given. Before
deleting, ports and servers are listed again and each port is only deleted if
it is still orphaned and unchanged (Neutron revision check). Pass --dry-run
to simulate deletion without modifying any resources.

Examples:
  OS_CLOUD=powervc-ss-ipi scripts/find-orphaned-ports.py
  scripts/find-orphaned-ports.py --cloud powervc-ss-ipi --prefix rdr-openstack-mbkxt-
  scripts/find-orphaned-ports.py --cloud powervc-ss-ipi --min-age-hours 2 --delete
  scripts/find-orphaned-ports.py --cloud powervc-ss-ipi --delete --dry-run
"""

import argparse
import datetime
import ipaddress
import json
import os
import sys

try:
    import openstack
except ImportError:
    sys.exit("error: the openstacksdk Python package is required (pip install openstacksdk)")


def parse_args():
    parser = argparse.ArgumentParser(
        description="Find orphaned Neutron ports on PowerVC/OpenStack.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Examples:" + __doc__.split("Examples:", 1)[1],
    )
    parser.add_argument("--cloud", default=os.environ.get("OS_CLOUD"),
                        help="cloud name from clouds.yaml (default: $OS_CLOUD)")
    parser.add_argument("--prefix", default="",
                        help="only consider ports whose name starts with this prefix "
                             "(e.g. an infraID followed by '-')")
    parser.add_argument("--network", default="",
                        help="only consider ports on this network (name or ID)")
    parser.add_argument("--min-age-hours", type=float, default=1.0,
                        help="only report ports created at least this many hours ago, "
                             "to avoid racing an install that is still running "
                             "(default: 1; 0 disables the check)")
    parser.add_argument("--include-unnamed", action="store_true",
                        help="also report ports with an empty name (skipped by default)")
    parser.add_argument("--json", action="store_true", help="print results as JSON")
    parser.add_argument("--delete", action="store_true", help="delete the reported ports")
    parser.add_argument("--dry-run", action="store_true",
                        help="simulate deletion without making changes")
    parser.add_argument("--yes", action="store_true",
                        help="do not prompt before deleting (requires --delete)")
    args = parser.parse_args()
    if not args.cloud:
        parser.error("no cloud given: use --cloud or set OS_CLOUD")
    if args.yes and not args.delete:
        parser.error("--yes only skips the confirmation prompt; add --delete to delete ports")
    if args.min_age_hours < 0:
        parser.error("--min-age-hours must not be negative")
    return args


def parse_time(value):
    """Parse a Neutron timestamp such as 2026-10-09T23:24:29Z (fractional seconds optional)."""
    if not value:
        return None
    value = value.replace("Z", "+00:00")
    try:
        parsed = datetime.datetime.fromisoformat(value)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=datetime.timezone.utc)
    return parsed


def scan(conn):
    """List the project's ports, the address pairs they reference and its servers.

    Address pairs are collected from every port visible to the credentials,
    so a VIP referenced from another project is still protected; only ports
    of the current project are returned as candidates.

    Ports are listed before servers so that a server created during the scan
    is included in the server list rather than making its port look stale.
    """
    project_id = conn.current_project_id
    visible = list(conn.network.ports())
    pairs = []
    for port in visible:
        for pair in port.allowed_address_pairs or []:
            try:
                pairs.append((port.id, ipaddress.ip_network(pair.get("ip_address", ""), strict=False)))
            except ValueError:
                continue
    ports = [port for port in visible if port.project_id == project_id]
    server_ids = {server.id for server in conn.compute.servers()}
    return ports, pairs, server_ids


def server_exists(conn, server_id):
    """Look a server up by ID; this also finds servers outside the project list."""
    try:
        conn.compute.get_server(server_id)
    except openstack.exceptions.NotFoundException:
        return False
    return True


def is_referenced(port, pairs):
    """Return True if another port's allowed_address_pairs covers one of this port's IPs."""
    for fixed_ip in port.fixed_ips or []:
        try:
            address = ipaddress.ip_address(fixed_ip.get("ip_address", ""))
        except ValueError:
            continue
        for owner_id, network in pairs:
            if owner_id != port.id and address in network:
                return True
    return False


def classify(conn, port, pairs, server_ids):
    """Return the orphan reason for a port, or None if it is in use.

    Only ports with no owner or a compute owner are considered; ports owned by
    Neutron or other services (Octavia, trunks, bare metal, ...) are left alone.
    A compute port missing from the server list is confirmed with a direct
    lookup before it is reported as stale.
    """
    owner = port.device_owner or ""
    if owner and not owner.startswith("compute:"):
        return None
    if is_referenced(port, pairs):
        return None
    if not port.device_id:
        return "unattached"
    if owner and port.device_id not in server_ids and not server_exists(conn, port.device_id):
        return "stale"
    return None


def find_orphans(conn, args):
    if not conn.current_project_id:
        raise openstack.exceptions.SDKException("credentials are not scoped to a project")
    network_id = None
    if args.network:
        network_id = conn.network.find_network(args.network, ignore_missing=False).id

    ports, pairs, server_ids = scan(conn)
    now = datetime.datetime.now(datetime.timezone.utc)
    min_age = datetime.timedelta(hours=args.min_age_hours)

    orphans = []
    for port in ports:
        if network_id and port.network_id != network_id:
            continue
        name = port.name or ""
        if args.prefix and not name.startswith(args.prefix):
            continue
        if not name and not args.include_unnamed:
            continue
        created = parse_time(port.created_at)
        if min_age and (created is None or now - created < min_age):
            continue
        reason = classify(conn, port, pairs, server_ids)
        if reason is None:
            continue
        orphans.append({
            "id": port.id,
            "name": name,
            "reason": reason,
            "status": port.status,
            "device_owner": port.device_owner or "",
            "device_id": port.device_id or "",
            "created_at": port.created_at or "",
            "description": port.description or "",
            "fixed_ips": [ip.get("ip_address", "") for ip in (port.fixed_ips or [])],
        })
    orphans.sort(key=lambda p: (p["name"], p["created_at"]))
    return orphans


def print_table(orphans):
    if not orphans:
        print("No orphaned ports found.")
        return
    headers = ("ID", "NAME", "REASON", "STATUS", "CREATED", "IPS", "DESCRIPTION")
    rows = [(p["id"], p["name"], p["reason"], p["status"], p["created_at"],
             ",".join(p["fixed_ips"]), p["description"]) for p in orphans]
    widths = [max(len(str(r[i])) for r in rows + [headers]) for i in range(len(headers))]
    for row in [headers] + rows:
        print("  ".join(str(col).ljust(widths[i]) for i, col in enumerate(row)).rstrip())
    print(f"\n{len(orphans)} orphaned port(s).")


def delete_ports(conn, orphans, assume_yes, dry_run=False, out=sys.stdout):
    """Delete orphaned ports; progress goes to out so it never mixes with JSON output."""
    if not orphans:
        return 0
    if dry_run:
        for port in orphans:
            print(f"Would delete port {port['id']} ({port['name']})", file=out)
        return 0
    if not assume_yes:
        if not sys.stdin.isatty():
            print("error: refusing to delete without confirmation; pass --yes", file=sys.stderr)
            return 1
        print(f"Delete {len(orphans)} port(s)? [y/N] ", end="", file=out, flush=True)
        try:
            answer = input().strip().lower()
        except EOFError:
            answer = ""
        if answer not in ("y", "yes"):
            print("Aborted.", file=out)
            return 0
    try:
        ports, pairs, server_ids = scan(conn)
    except openstack.exceptions.SDKException as err:
        print(f"error: failed to re-check ports before deleting: {err}", file=sys.stderr)
        return 1
    current = {port.id: port for port in ports}

    failures = 0
    for port in orphans:
        fresh = current.get(port["id"])
        if fresh is None:
            print(f"Skipped port {port['id']} ({port['name']}): already deleted", file=out)
            continue
        try:
            if classify(conn, fresh, pairs, server_ids) is None:
                print(f"Skipped port {port['id']} ({port['name']}): no longer orphaned", file=out)
                continue
            # if_revision makes Neutron refuse the delete if the port changed after the re-check.
            conn.network.delete_port(port["id"], ignore_missing=True,
                                     if_revision=fresh.revision_number)
            print(f"Deleted port {port['id']} ({port['name']})", file=out)
        except openstack.exceptions.PreconditionFailedException:
            print(f"Skipped port {port['id']} ({port['name']}): changed during deletion", file=out)
        except openstack.exceptions.SDKException as err:
            failures += 1
            print(f"error: failed to delete port {port['id']} ({port['name']}): {err}", file=sys.stderr)
    return 1 if failures else 0


def main():
    args = parse_args()
    try:
        conn = openstack.connect(cloud=args.cloud)
        orphans = find_orphans(conn, args)
    except openstack.exceptions.SDKException as err:
        print(f"error: {err}", file=sys.stderr)
        return 1

    if args.json:
        print(json.dumps(orphans, indent=2))
    else:
        print_table(orphans)

    if args.delete or args.dry_run:
        return delete_ports(conn, orphans, args.yes, dry_run=args.dry_run,
                            out=sys.stderr if args.json else sys.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
