# The Founder & The Philosophy

> **"The cloud became an estate to operate. Kitwork is a disagreement."**
> — Huỳnh Nhân Quốc, Founder & Creator

Kitwork is not just a software engine; it is a declaration of independence. It was built by a single developer seeking to free backend logic from the weight of modern infrastructure, containers, and complex build toolchains.

---

## About the Founder

**Huỳnh Nhân Quốc** is a cloud infrastructure engineer born in Tam Kỳ, Quảng Nam, Vietnam. His journey into systems programming is an unconventional story of self-teaching, discipline, and the pursuit of engineering sovereignty.

### Timeline of the Journey

```mermaid
chronology
    2015 : First HTML/CSS tests on Blogger
         : 2-year Military Service completed (Discipline & self-reliance)
         : Entered Quang Nam University (Philosophy & C# shoes sales system)
    2018 : Dropped out of university & moved to Saigon (Angular Developer)
         : SEO failures & deep-dives into search algorithms
    2019 : Go developer trial in Danang
         : Co-founded DIMODO with Korean & Chinese partners (Communicated via Google Translate)
    2020 : COVID-19 pandemic separation (DIMODO dissolved)
         : Returned to Tam Kỳ, ran Giao Vặt Tam Kỳ (15k deliveries) to pay for server VPS
    2021 : Built Samdy.vn (Affiliate comparative engine) — Ranked top 66 e-commerce sites in VN
    2022 : Pivoted to Highlands Coffee ads automation — Earned $10,000 in 3 months
         : Built Hoa House Cafe ("Ở đây có bán mộng mơ")
    2025 : Tech independence at age 30 & began building Kitwork Engine in Go
```

---

## Core Philosophy of Kitwork

Every line of code in Kitwork is guided by three principles born out of the founder's real-world trials:

### 1. Roadmap = README
When Huỳnh Nhân Quốc wrote the first lines of the Kitwork `README.md`, many in the developer community dismissed it as an unrealistic dream. To him, the README was not a technical manual but a **blueprint for life and technology** — a statement of intent that replaces traditional university degrees with raw curiosity and execution. 

### 2. Logic Sovereignty
Logic should be portable, deterministic, and free from the weight of modern infrastructure. Modern cloud development has become too complex — forcing developers to write simple code but spend months configuring Docker, Kubernetes, Redis, and separate database clusters. 
Kitwork collapses this entire estate back into **one runtime with one philosophy**:
- The custom stack-based VM prevents infinite loops by design.
- The single Go binary hosts serverless compute, routing, edge proxying, and database access natively.
- Deployment is as simple as dropping a directory (`tenants/<identity>/<domain>/`).

### 3. "Hãy ra ngoài khi trời còn sáng"
*("Go outside while it is still light")*
A quote that serves as a reminder to step away from the glowing screen and enjoy the sunlight after long, continuous stretches of overnight coding. It reflects the philosophy that technology, when built with heart and simple beauty, should serve human happiness and simplicity, rather than creating operational nightmares.

> **"A product can fail, but a strong platform makes it very difficult for that to happen."**
> — Huỳnh Nhân Quốc

---

## Technical Achievements & VM Design

### The 58ms Performance Threshold
In response to community debates around virtual machine benchmarks, Huỳnh Nhân Quốc undertook "open-heart surgery" on the VM core. By rewriting the execution loop and variables scope model, he achieved a critical performance threshold:
- **1,000,000 stack operations executed in 58ms** (~17,000,000 VM instructions per second).
- **Zero dynamic memory allocation (0 B/op)** on hot paths, completely eliminating Go Garbage Collector pressure.

### Core Architectural Decisions
1. **Static Slot Allocation (Removing `map[string]interface{}`)**: Variable resolution is executed at compile time. Variable names are replaced by fixed integer slots in bytecode instructions, enabling O(1) constant-time value array offsets at runtime.
2. **Pre-allocated Value Stack**: Contiguous value slices prevent pointer chasing and keep stack values warm in L1/L2 caches, dropping cache misses to near-zero.
3. **VM Pooling & Re-usability**: Context structures are cached via Go's `sync.Pool`. Preserved slices are reset inside recycled VMs rather than reallocated.

---

## The Founder's Story: Chasing Technological Sovereignty

### Chapter I: The Old Laptop and the $15 Delivery Trips
Five years ago, Huỳnh Nhân Quốc returned to his hometown of Tam Kỳ with nothing but an aging laptop, worn-out keys, and a head full of ideas. To fund the VPS servers hosting his experimental projects, he rode his motorbike through the sweltering heat of Central Vietnam doing deliveries for `Giao Vặt Tam Kỳ` at 15,000 VND (less than $1) per trip.

For him, honest labor to feed a technical passion was a badge of honor. He eventually built and optimized comparative pricing platforms like `Samdy.vn` (which briefly entered the Top 66 e-commerce websites in Vietnam) and automated Highlands Coffee voucher ads. The proceeds from these endeavors funded **Hoa House Cafe** ("Ở đây có bán mộng mơ" — *We sell dreams here*), a quiet physical space in Tam Kỳ designed to support his independent software research.

### Chapter II: Compilers, Bytecode, and the Nanosecond VM
The natural evolution of his quest for independence led him from UI rendering to virtual machines and custom execution engines. Challenged by community debates on VM performance, he undertook critical optimization work on the Kitwork virtual machine.

By eliminating dynamic memory lookups (replacing `map[string]interface{}` with **Static Slot Allocation**), keeping the stack pre-allocated and contiguous to minimize CPU cache misses, and pooling execution contexts via `sync.Pool`, he achieved a landmark benchmark: **1,000,000 stack operations executed in 58 milliseconds** under a strict Zero GC constraint. To him, bytecode is more than a low-level format; it represents a philosophy of conservation—reducing CPU power waste (**Energy Computing**) and letting logic flow as smoothly as water.

### Chapter III: Kitwork Cluster & Unified Runtimes
Modern cloud environments have become overly complex estates of containers, coordinators, and databases. Kitwork is a rebellion against this overhead.

He designed the Kitwork Cluster around a unified runtime model—**"One Runtime, Many Roles"**—where a single executable binary dynamically handles worker execution, routing gateways, and coordinator state depending on network load. This represents his ultimate goal of **Technological Independence**: returning the full sovereignty of the cloud back to the solo developer.

### Chapter IV: "Hãy Ra Ngoài Khi Trời Còn Sáng"
Technology, when built with heart, should serve human happiness and simplicity. This final chapter of the developer's journey is not written in code, but in living. The philosophy of stepping away from the server room to see the sun, to brew coffee at Hoa House Cafe, or to play acoustic guitar represents the balance needed to sustain long-term creativity. It is the human heart behind the machine.

---

## Author & Community
- **GitHub**: [@huynhnhanquoc](https://github.com/huynhnhanquoc)
- **Substack**: [huynhnhanquoc.substack.com](https://huynhnhanquoc.substack.com)
- **Personal Blog**: [huynhnhanquoc.com](https://huynhnhanquoc.com)
- **Open Source Projects**: [Kit Module](https://kitmodule.vn)
