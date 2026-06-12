# Kitwork

> One Runtime. Many Roles. One Source of Truth.

Most frameworks begin with code.

Kitwork begins with a question.

## Why does software become more complicated as it grows?

At the beginning, everything is simple.

A single application.

A single database.

A single server.

Then reality arrives.

We need caching.

We add Redis.

We need queues.

We add RabbitMQ.

We need events.

We add Kafka.

We need discovery.

We add Consul.

We need orchestration.

We add Kubernetes.

Every technology solves a problem.

Every decision makes sense.

Yet somehow, years later, the infrastructure becomes larger than the application itself.

The team spends more time operating the system than improving the product.

The architecture becomes a collection of technologies rather than a coherent platform.

Kitwork was born from the belief that there may be another way.

---

# Runtime Before Framework

Most frameworks think in terms of applications.

Kitwork thinks in terms of Runtime.

A framework answers:

> How should code be written?

A Runtime answers:

> How should code live?

Execution.

Scheduling.

State.

Coordination.

Resilience.

Failure.

Recovery.

These are Runtime concerns.

And these concerns exist long before a web framework, API router, or template engine enters the picture.

---

# One Runtime

Kitwork intentionally avoids creating different kinds of servers.

There is no special Gateway server.

There is no special Coordinator server.

There is no special Worker server.

There is only:

**Kitwork Runtime**

Every node runs the same engine.

Every node understands the same language.

Every node shares the same capabilities.

The only thing that changes is responsibility.

```text
Gateway Runtime

Gateway
Coordinator
Worker


Coordinator Runtime

Coordinator
Worker


Worker Runtime

Worker
```

This distinction is fundamental.

A role defines responsibility.

A Runtime defines capability.

Responsibilities may change.

Capabilities remain.

---

# A Runtime Never Becomes Useless

Traditional systems often divide responsibilities into isolated components.

A Gateway routes traffic.

A Worker executes tasks.

A Coordinator manages the cluster.

Each component has a single purpose.

Each component becomes vulnerable to its own failure.

Kitwork takes a different approach.

Every Runtime remains capable.

Every Runtime remains useful.

Every Runtime can adapt.

When Workers become unavailable:

```text
Coordinator → Execute
```

When Coordinators become unavailable:

```text
Gateway → Execute
```

When parts of the cluster disappear:

The remaining Runtime continues.

Not because it was designed for perfection.

But because it was designed for survival.

---

# Failure Is Not An Exception

Most architectures treat failure as an edge case.

Distributed systems teach a different lesson.

Machines fail.

Networks fail.

Storage fails.

Humans fail.

Failure is not exceptional.

Failure is inevitable.

The goal is not to eliminate failure.

The goal is to continue operating despite failure.

This philosophy influences every decision inside Kitwork.

The question is never:

> How do we prevent failure?

The question is:

> What happens next?

---

# Progressive Degradation

Kitwork follows a simple principle:

Lose efficiency before losing availability.

Normal operation:

```text
Client
  ↓
Gateway
  ↓
Coordinator
  ↓
Worker
```

Worker unavailable:

```text
Client
  ↓
Gateway
  ↓
Coordinator Execute
```

Coordinator unavailable:

```text
Client
  ↓
Gateway Execute
```

Severe cluster failure:

```text
Client
  ↓
Gateway
  ↓
Database Cluster
```

Performance may decrease.

The system continues.

The Runtime adapts.

---

# Database Cluster

Every distributed system requires memory.

Not memory in RAM.

Memory in identity.

The memory of what the system is.

The memory of what happened.

The memory of what must survive.

Kitwork calls this:

**Database Cluster**

It stores:

* Business Data
* Cluster State
* Tenant State
* Jobs
* Events
* Metadata
* Configuration

The Runtime may come and go.

The state remains.

Machines are temporary.

State is permanent.

The cluster is not defined by the servers that run it.

The cluster is defined by the state it preserves.

---

# Cluster Bus

A cluster is not a collection of machines.

A cluster is a conversation.

Nodes communicate.

Nodes coordinate.

Nodes adapt.

Nodes evolve.

This communication layer is the Cluster Bus.

It carries:

* Heartbeats
* Elections
* Scheduling
* Tenant Migration
* Cache Invalidation
* Configuration Updates
* System Events

The Database Cluster is the memory.

The Cluster Bus is the nervous system.

The Runtime is the organism.

Together they become a living platform.

---

# Beyond Infrastructure

Kitwork is not trying to compete with Redis.

Kitwork is not trying to replace Kafka.

Kitwork is not trying to become another framework.

Those are implementation details.

The real objective is much simpler.

To build a platform where every component shares the same philosophy.

A platform that remains understandable after years of growth.

A platform that remains operable after years of complexity.

A platform that values continuity over perfection.

---

# Philosophy

When people talk about:

> Microservices vs Monolith

We talk about Runtime.

When people talk about:

> Frameworks

We talk about Execution.

When people talk about:

> Infrastructure

We talk about Survival.

Because software is not defined by how it starts.

Software is defined by how long it can continue moving forward.

---

# Final Thought

A strong system is not a system that never fails.

A strong system is a system that continues operating after failure occurs.

Kitwork exists to explore that idea.

One Runtime.

Many Roles.

One Source of Truth.
