# Live Context

## The problem

You've ingested the Kubernetes documentation, the Terraform docs, and your runbooks. You ask nexus "why are my pods crashing?" It gives you a well-cited answer from your books — but it has no idea what your actual pods are doing *right now*.

That's the gap `nexus context` closes.

---

## What it does

`nexus context` lets you register shell commands. Before generating any query answer, nexus runs all registered commands, captures their output, and injects it into the LLM prompt alongside your static document chunks.

The LLM sees both:
- `[1] Kubernetes docs — Pod lifecycle` (from your ingested books)
- `[live:kubectl] $ kubectl get pods -A` (your actual cluster state, just captured)

It can then answer "why are your pods crashing" with *your pods*, not a generic explanation.

---

## When to use it

Live context is useful when:

- The question is about your specific environment, not general knowledge
- The state changes frequently enough that a static snapshot would be stale
- You want the AI to cross-reference documentation with reality

**Examples:**
- "Why is CoreDNS restarting?" → needs current pod status + networking docs
- "Is my Terraform state consistent with what's deployed?" → needs `terraform show` output + your TF code
- "What's consuming the most memory in my cluster?" → needs Prometheus metrics

---

## Setting up infrastructure sources

### Kubernetes

```bash
# Current pod status across all namespaces
nexus context add kubectl "kubectl get pods -A" --description "pod status"

# Node status and resources
nexus context add nodes "kubectl get nodes -o wide" --description "node overview"

# Recent events (errors bubble up here)
nexus context add events "kubectl get events --sort-by=.lastTimestamp -A | tail -30" \
    --description "recent cluster events"

# A specific namespace
nexus context add app-pods "kubectl get pods -n my-app -o wide" \
    --description "pods in my-app namespace"
```

### Terraform

```bash
# Current state summary
nexus context add tf "terraform show | head -100" --description "Terraform state"

# Pending changes
nexus context add tf-plan "cd ~/infra && terraform plan -no-color 2>&1 | tail -40" \
    --description "pending Terraform changes"
```

### Prometheus / Grafana

```bash
# Query Prometheus directly
nexus context add memory \
    "curl -s 'http://localhost:9090/api/v1/query?query=container_memory_usage_bytes{namespace=\"default\"}' | jq '.data.result[] | {pod: .metric.pod, bytes: .value[1]}'" \
    --description "container memory usage"

# Node CPU
nexus context add cpu \
    "curl -s 'http://localhost:9090/api/v1/query?query=1-avg(rate(node_cpu_seconds_total{mode=\"idle\"}[5m]))by(instance)' | jq '.data.result'"
```

### System

```bash
# Load and uptime
nexus context add load "uptime && df -h | head -10" --description "system load and disk usage"

# Docker containers
nexus context add containers "docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'" \
    --description "running containers"
```

---

## Verifying a source works

Before relying on a source in queries, test it:

```bash
nexus context run kubectl
```

This runs the command and prints its output without triggering a full query. Fix any issues here before connecting them to queries.

Common issues:
- **Command not found**: the shell nexus uses may not have your PATH. Use absolute paths (`/usr/local/bin/kubectl`) or set PATH explicitly in the command.
- **Authentication expired**: `kubectl` contexts, AWS credentials, and similar things expire. nexus can't refresh them — the command just fails silently (logged as a warning).
- **Too much output**: the LLM has a context window limit. If a command produces thousands of lines, the context gets overwhelmed. Pipe through `head -N` or `jq` to extract only what matters.

---

## How it works in a query

When you run `nexus query "..."`:

1. The question is embedded and a vector search runs (same as always)
2. All registered context sources run **concurrently** with a **5-second timeout** per command
3. Successful outputs are injected into the prompt as `[live:name]` sections
4. Failed commands are logged as warnings but do not block the query — you still get an answer from static sources
5. The LLM sees live context first, then static chunks
6. The LLM is instructed to prefer live data over static when they conflict

The 5-second timeout is per-command but all commands run in parallel. A query with 3 registered sources waits at most 5 seconds total for live context (not 15).

### Skipping live context

```bash
nexus query "..." --no-live
```

Use this when:
- You want a fast answer and don't need current state
- Your Kubernetes cluster is unreachable right now
- You're debugging the static knowledge base specifically

---

## In the LLM prompt

The prompt nexus sends to `llama3.1:8b` looks like this (simplified):

```
You are a knowledgeable assistant...
Always answer in English.
Prefer live context over static sources when they conflict.

Question: why is coredns crashing?

Live Context (current state of your environment):
[live:kubectl] $ kubectl get pods -A
kube-system   coredns-abc123   0/1   CrashLoopBackOff   12   4h

[live:events] $ kubectl get events -A | tail -30
kube-system   Warning   BackOff   pod/coredns-abc123   Back-off restarting failed container

Knowledge Base:
[1] Kubernetes in Action — DNS and service discovery
...CoreDNS is the default DNS server in Kubernetes clusters...

[2] Kubernetes docs — Troubleshooting pods
...CrashLoopBackOff means the container is repeatedly failing to start...

Answer:
```

The LLM now has both your pods' actual state and the relevant documentation — it can give a specific, grounded answer.

---

## Limitations

**Commands run at query time.** There is no caching. If your Prometheus query takes 3 seconds, every query that includes it waits 3 seconds. This is acceptable for a personal tool; production use would add caching.

**No authentication handling.** nexus runs commands exactly as written. If `kubectl` needs a `--kubeconfig` flag, include it in the command. If credentials expire, the command fails and the warning appears in logs.

**Output must be text.** Commands that produce binary output or ANSI color codes may confuse the LLM. Pipe through `jq` or `--no-color` flags where needed.

**Context window limits.** If many sources produce long output, the total context sent to the LLM may exceed what `llama3.1:8b` handles well. Keep individual command outputs concise — under 100 lines is a good guideline.
