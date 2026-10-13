#!/usr/bin/env python3
"""Behavioral server acceptance checks on an explicitly disposable stand.

Requires a safe SSH helper, an expected Docker engine ID and an output directory.
The helper must use configured credential references without exposing values.
No raw configuration, environment, cookies or command output is written to logs.
"""
import argparse
import base64
import json
from pathlib import Path
import shlex
import subprocess
import time


class Stand:
    def __init__(self, helper, engine_id, output):
        self.helper = helper
        self.output = Path(output)
        self.output.mkdir(parents=True, exist_ok=True)
        self.events = []
        actual = self.remote("docker info --format '{{.ID}}'").stdout.strip()
        if actual != engine_id:
            raise RuntimeError("Docker engine ID does not match the authorized stand")
        self.record("authorized-engine", engineID=actual)

    def remote(self, command, timeout=900, check=True):
        result = subprocess.run([self.helper, command], capture_output=True,
                                text=True, timeout=timeout)
        if check and result.returncode:
            # Command output may contain sensitive diagnostics: never echo it.
            raise RuntimeError("Remote operation failed (exit %d)" % result.returncode)
        return result

    def cli(self, *args, check=True):
        return self.remote(shlex.join(["citeck", *args]), check=check)

    def python(self, code):
        encoded = base64.b64encode(code.encode()).decode()
        command = "python3 -c " + shlex.quote(
            "import base64;exec(base64.b64decode(%r))" % encoded)
        return self.remote(command)

    def api(self, method, path, body=None):
        code = """import http.client,socket,json
class UnixHTTP(http.client.HTTPConnection):
 def connect(self):
  self.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
  self.sock.settimeout(30);self.sock.connect('/run/citeck/daemon.sock')
c=UnixHTTP('localhost');c.request(%r,%r,body=%r)
r=c.getresponse();raw=r.read();c.close()
try:data=json.loads(raw)
except ValueError:data={'nonJSONBytes':len(raw)}
print(json.dumps({'status':r.status,'data':data}))
""" % (method, path, body)
        return json.loads(self.python(code).stdout)

    def status(self):
        return json.loads(self.cli("status", "--format", "json").stdout)

    def wait(self, namespace="RUNNING", app=None, app_status=None, timeout=900):
        end = time.monotonic() + timeout
        last = None
        while time.monotonic() < end:
            result = self.cli("status", "--format", "json", check=False)
            if result.returncode:
                time.sleep(5)
                continue
            current = json.loads(result.stdout)
            states = {item["name"]: item["status"] for item in current["apps"]}
            wanted = (states.get(app) == app_status) if app else (
                current["status"] == namespace and all(
                    value in (("STOPPED",) if namespace == "STOPPED" else ("RUNNING", "STOPPED"))
                    for value in states.values()))
            progress = {"namespace": current["status"], "states": states}
            if progress != last:
                print(json.dumps({"progress": progress}), flush=True)
                last = progress
            if wanted:
                return current
            if any("FAIL" in value for value in states.values()):
                raise RuntimeError("An application reached a failed state")
            time.sleep(5)
        raise RuntimeError("Timed out waiting for server state")

    def record(self, name, **details):
        event = {"name": name, "passed": True, **details}
        self.events.append(event)
        line = json.dumps(event)
        print(line, flush=True)
        with (self.output / "events.jsonl").open("a") as stream:
            stream.write(line + "\n")

    def containers(self):
        code = """import subprocess,json
ids=subprocess.check_output(['docker','ps','-aq','--filter','label=citeck.launcher=true'],text=True).split()
items=json.loads(subprocess.check_output(['docker','inspect',*ids])) if ids else []
print(json.dumps({(c['Config'].get('Labels') or {}).get('citeck.launcher.app.name'):{'id':c['Id'],'startedAt':c['State']['StartedAt']} for c in items}))
"""
        return json.loads(self.python(code).stdout)

    def mailpit(self):
        self.python("""import subprocess,json,smtplib,urllib.request,time,uuid
from email.message import EmailMessage
c=json.loads(subprocess.check_output(['docker','inspect','citeck_mailpit_default']))[0]
assert not c['HostConfig']['PortBindings'], 'Server SMTP must remain internal'
ip=next(iter(c['NetworkSettings']['Networks'].values()))['IPAddress']
subject='launcher-acceptance-'+uuid.uuid4().hex
m=EmailMessage();m['From']='sender@example.invalid';m['To']='recipient@example.invalid';m['Subject']=subject;m.set_content('Synthetic server acceptance message')
with smtplib.SMTP(ip,1025,timeout=20) as smtp: smtp.send_message(m)
base='http://'+ip+':8025/api/v1/'
found=[]
for _ in range(20):
 with urllib.request.urlopen(base+'messages',timeout=20) as r: data=json.load(r)
 found=[v for v in data['messages'] if v['Subject']==subject]
 if found:break
 time.sleep(1)
assert len(found)==1
with urllib.request.urlopen(base+'message/'+found[0]['ID'],timeout=20) as r: actual=json.load(r)
assert 'Synthetic server acceptance message' in actual['Text']
req=urllib.request.Request(base+'messages',data=json.dumps({'IDs':[found[0]['ID']]}).encode(),headers={'Content-Type':'application/json'},method='DELETE')
with urllib.request.urlopen(req,timeout=20) as r: assert r.status==200
print(json.dumps({'passed':True,'checks':['internal-only-ports','real-SMTP-delivery','API-body-match','owned-message-cleanup']}))
""")
        self.record("mailpit-internal-SMTP-delivery-and-owned-cleanup")

    def sql(self, statement):
        return self.cli("exec", "postgres", "--", "psql", "-U", "postgres",
                        "-d", "postgres", "-At", "-v", "ON_ERROR_STOP=1", "-c", statement).stdout


def check(value, message):
    if not value:
        raise RuntimeError(message)


def acceptance(stand, exercise, snapshots):
    current = stand.wait()
    check(current["status"] == "RUNNING", "Namespace not running")
    check(all(a["status"] == "RUNNING" for a in current["apps"]),
          "A clean default installation unexpectedly has stopped applications")
    stand.record("cold-start-all-applications", apps=len(current["apps"]))
    catalog = json.loads(stand.cli("agent").stdout)
    check(catalog["mode"] == "server", "Wrong runtime mode")
    routes = {r["method"] + " " + r["path"] for r in catalog["routes"]}
    check("POST /desktop/focus" not in routes and
          "POST /api/v1/desktop/update/apply" not in routes,
          "Desktop routes leaked into server discovery")
    stand.record("server-agent-discovery", routes=len(routes),
                 operations=len(catalog["operations"]))
    for path in ["/api/v1/workspaces", "/api/v1/desktop/update/status",
                 "/api/v1/agent/operations/does-not-exist"]:
        check(stand.api("GET", path)["status"] == 404, "Expected unavailable API")
    stand.record("unavailable-and-unknown-APIs-refused")
    stand.cli("config", "validate")
    health = json.loads(stand.cli("health", "--format", "json").stdout)
    check(health["healthy"], "Clean installation health is not healthy")
    stand.record("config-validation-and-health")
    config_hash = stand.python("import hashlib;print(hashlib.sha256(open('/opt/citeck/conf/namespace.yml','rb').read()).hexdigest())").stdout.strip()
    invalid = stand.api("PUT", "/api/v1/config", "invalid: [")
    check(invalid["status"] == 400, "Malformed configuration accepted")
    after_hash = stand.python("import hashlib;print(hashlib.sha256(open('/opt/citeck/conf/namespace.yml','rb').read()).hexdigest())").stdout.strip()
    check(config_hash == after_hash, "Invalid edit changed persisted configuration")
    stand.record("invalid-config-preserves-file")
    plan = json.loads(stand.cli("reload", "--dry-run", "--format", "json").stdout)
    check(all(plan["summary"][key] == 0 for key in ("create", "recreate", "remove")),
          "Unchanged reload plans container changes")
    stand.record("reload-dry-run-unchanged", summary=plan["summary"])
    before = stand.containers()
    stand.cli("reload", "--detach")
    stand.wait()
    check(stand.containers() == before, "Unchanged reload recreated containers")
    stand.record("unchanged-reload-preserves-containers")
    check(stand.cli("logs", "mailpit", "--tail", "5").stdout.strip(), "No application logs")
    description = json.loads(stand.cli("describe", "mailpit", "--format", "json").stdout)
    check(bool(description), "No application description")
    stand.record("logs-and-describe")
    missing = stand.cli("snapshot", "import", "nonexistent-acceptance", "--yes", check=False)
    check(missing.returncode != 0 and stand.status()["status"] == "RUNNING",
          "Missing snapshot import changed namespace state")
    stand.record("missing-snapshot-refused-before-stop")
    if not exercise:
        return
    stand.cli("stop", "mailpit")
    state = stand.wait(app="mailpit", app_status="STOPPED")
    check(next(a for a in state["apps"] if a["name"] == "mailpit")["detached"],
          "Manual stop did not detach application")
    stand.cli("reload", "--detach")
    time.sleep(5)
    state = stand.status()
    check(next(a for a in state["apps"] if a["name"] == "mailpit")["status"] == "STOPPED",
          "Reload reattached a manually stopped application")
    stand.record("manual-stop-persists-through-reload")
    stand.cli("start", "mailpit", "--detach")
    stand.wait(app="mailpit", app_status="RUNNING")
    stand.record("explicit-app-start-reattaches")
    stand.mailpit()
    old_eapps = stand.containers()["eapps"]
    stand.cli("restart", "eapps", "--detach")
    stand.wait(app="eapps", app_status="RUNNING")
    check(stand.containers()["eapps"] != old_eapps, "Application restart did not restart its container")
    stand.record("application-restart")
    stand.sql("CREATE TABLE IF NOT EXISTS launcher_acceptance_marker (id integer PRIMARY KEY); INSERT INTO launcher_acceptance_marker VALUES (1) ON CONFLICT DO NOTHING;")
    check("1" in stand.sql("SELECT id FROM launcher_acceptance_marker ORDER BY id;"),
          "Database marker missing")
    before = stand.containers()
    stand.remote("systemctl restart citeck")
    stand.wait()
    check(stand.containers() == before, "Daemon restart changed container IDs/start times")
    stand.record("daemon-restart-preserves-containers")
    if snapshots:
        stand.cli("stop", "--detach")
        stand.wait(namespace="STOPPED")
        stand.cli("snapshot", "export", "--yes")
        inventory = stand.api("GET", "/api/v1/snapshots")["data"]
        check(len(inventory) == 1, "Unexpected exported snapshot count")
        name = inventory[0]["name"]
        inspect = "import zipfile,json; p='/opt/citeck/data/runtime/default/snapshots/'+%r; z=zipfile.ZipFile(p); print(json.dumps({'entries':len(z.namelist()),'hasSecrets':'secrets.json' in z.namelist()}))" % name
        metadata = json.loads(stand.python(inspect).stdout)
        check(not metadata["hasSecrets"], "Default export included secrets")
        stand.record("snapshot-export-stopped-no-secrets", **metadata)
        stand.cli("start", "--detach")
        stand.wait()
        stand.sql("INSERT INTO launcher_acceptance_marker VALUES (2) ON CONFLICT DO NOTHING;")
        check("2" in stand.sql("SELECT id FROM launcher_acceptance_marker ORDER BY id;"),
              "Post-snapshot marker missing")
        stand.cli("stop", "--detach")
        stand.wait(namespace="STOPPED")
        stand.python("from pathlib import Path; p=Path('/opt/citeck/data/runtime/default/snapshots/corrupt-acceptance.zip');p.write_bytes(b'not a ZIP archive')")
        failure = stand.cli("snapshot", "import", "corrupt-acceptance", "--yes", check=False)
        check(failure.returncode != 0 and stand.status()["status"] == "STOPPED",
              "Failed import automatically restarted namespace")
        stand.record("corrupt-snapshot-refused-no-auto-restart")
        stand.cli("snapshot", "import", name, "--yes")
        check(stand.status()["status"] == "STOPPED", "Stopped import started namespace")
        stand.cli("start", "--detach")
        stand.wait()
        rows = stand.sql("SELECT id FROM launcher_acceptance_marker ORDER BY id;")
        check(rows.strip() == "1", "Snapshot did not restore database contents")
        stand.python("from pathlib import Path; Path('/opt/citeck/data/runtime/default/snapshots/corrupt-acceptance.zip').unlink()")
        stand.record("snapshot-import-restores-real-database")
    else:
        stand.cli("stop", "--detach")
        stand.wait(namespace="STOPPED")
        stand.cli("start", "--detach")
        stand.wait()
        check("1" in stand.sql("SELECT id FROM launcher_acceptance_marker ORDER BY id;"),
              "Namespace stop/start lost database data")
        stand.record("namespace-stop-start-preserves-data")
    health = json.loads(stand.cli("health", "--format", "json").stdout)
    check(health["healthy"], "Final server health is not healthy")
    stand.record("final-health")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ssh-helper", required=True)
    parser.add_argument("--expected-engine-id", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--exercise-disposable-server", action="store_true")
    parser.add_argument("--snapshots", action="store_true")
    args = parser.parse_args()
    if args.snapshots and not args.exercise_disposable_server:
        parser.error("--snapshots requires --exercise-disposable-server")
    stand = None
    try:
        stand = Stand(args.ssh_helper, args.expected_engine_id, args.output)
        acceptance(stand, args.exercise_disposable_server, args.snapshots)
        (stand.output / "completed.json").write_text(json.dumps(
            {"passed": True, "checks": stand.events}, indent=2) + "\n")
    except Exception as error:
        # Do not retain raw subprocess/HTTP/exception text containing credentials.
        print(json.dumps({"passed": False, "errorType": type(error).__name__,
                          "lastPassedCheck": stand.events[-1]["name"] if stand and stand.events else None}), flush=True)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
