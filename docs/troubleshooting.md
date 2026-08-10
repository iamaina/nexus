# Troubleshooting

Recovery steps for the failure modes that have actually been hit. Each entry: symptom, root cause, recovery.

---

## `connect: connection refused` on port 5432

**Symptom**

```text
[ERROR] App initialization failed err=connect to database: failed to connect to `user=vaultuser database=opsnexus`:
    127.0.0.1:5432 (localhost): dial error: dial tcp 127.0.0.1:5432: connect: connection refused
```

`brew services list` shows `postgresql@14` with status `error`. `brew services restart postgresql@14` reports "Successfully started" but `lsof -i :5432` is empty.

**Root cause**

A previous postgres was killed with `kill -9` (or its parent was), so it never cleaned up `postmaster.pid`. The lock file still points to the now-dead PID. Postgres refuses to start because it thinks another postmaster is running. The log confirms this:

```text
FATAL:  lock file "postmaster.pid" already exists
HINT:  Is another postmaster (PID 798) running in data directory "/opt/homebrew/var/postgresql@14"?
```

The hinted PID may have been recycled by macOS to a completely unrelated process (e.g. `ctkd`), which is why the lock looks valid but is stale.

**Recovery**

1. Confirm the PID in the lock file is not actually postgres:

   ```bash
   cat /opt/homebrew/var/postgresql@14/postmaster.pid | head -1   # prints the PID
   ps -p <PID>                                                     # check what it is
   ```

   If the command is anything other than `postgres`, the lock is stale.

2. Remove the lock and restart:

   ```bash
   rm /opt/homebrew/var/postgresql@14/postmaster.pid
   brew services restart postgresql@14
   ```

3. Verify postgres is listening:

   ```bash
   lsof -i :5432
   ```

**Do not** remove `postmaster.pid` without verifying step 1. If a real postgres is running and you delete its lock, you can corrupt the data directory.

**Prevention**

Avoid `kill -9` on nexus while it has a child postgres. Use plain `kill` first so postgres can shut down cleanly. Only escalate to `-9` after confirming the process is unresponsive.
