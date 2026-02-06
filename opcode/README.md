# Kitwork Opcode: The Machine Language

> **"The Atomic Instructions of the Kitwork Virtual Machine."**

Kitwork Opcode is the Instruction Set Architecture (ISA) for the Kitwork Engine. It transforms high-level logic into a stream of optimized, stack-based bytecodes that execute with nanosecond precision.

Designed for efficiency, safety, and energy accountability.

---

## 🏗 Architecture Overview

Kitwork VM is a **Stack-based Virtual Machine**.
*   **Operands**: Most instructions consume values from the top of the Stack.
*   **Results**: Computations push results back onto the Stack.
*   **Zero-Address**: Instructions rarely carry data; they operate on the stack state.

---

## 📜 Instruction Set (ISA)

### 1. Data Flow (Memory & Stack)
Instructions to move data between the Stack and the Execution Context.

| Opcode | Description | Energy Cost |
| :--- | :--- | :--- |
| `PUSH` | Push a constant/literal value onto the Stack. | Low |
| `POP` | Remove the top value from the Stack. | Low |
| `DUP` | Duplicate the top value (for reuse). | Low |
| `LOAD` | Load a variable from Context/Memory to Stack. | Medium |
| `STORE` | Save top Stack value to Context/Memory. | Medium |
| `GET` | Access property (`obj.prop`) or index (`arr[i]`). | Medium |

### 2. Arithmetic & Logic (ALU)
Basic mathematical and boolean operations.

| Opcode | Description | Energy Cost |
| :--- | :--- | :--- |
| `ADD`, `SUB`, `MUL`, `DIV` | Basic arithmetic operations (`+`, `-`, `*`, `/`). | Low |
| `AND`, `OR` | Logical operators with short-circuiting support. | Low |
| `NOT` | Invert boolean value. | Low |
| `COMPARE` | Compare top two values (`==`, `!=`, `>`, etc.). | Low |

### 3. Control Flow (Branching)
Instructions that alter the execution path (Jumps).

| Opcode | Description | Energy Cost |
| :--- | :--- | :--- |
| `JUMP` | Unconditional jump to an instruction address. | Low |
| `TRUE` / `FALSE` | Conditional jump based on Stack top value. | Low |
| `ITER` | Optimized iterator for loops (Range/Foreach). | Medium |
| `HALT` | Immediate forced termination. | N/A |
| `YIELD` | Pause execution (Cooperative multitasking). | Low |

### 4. Structures & Memory
Creating and manipulating complex data types.

| Opcode | Description | Energy Cost |
| :--- | :--- | :--- |
| `MAKE` | Allocate new Map/Array from Memory Pool. | High |
| `SET` | Assign value to a key/index in a structure. | Medium |

### 5. Execution & Functions
Invoking logic and managing scope.

| Opcode | Description | Energy Cost |
| :--- | :--- | :--- |
| `CALL` | Invoke a Host Function (Go Native). | Varies |
| `INVOKE` | Call a method on an Object (`user.HasPermission()`). | Varies |
| `LAMBDA` | Initialize an anonymous function (Closure). | Medium |
| `RETURN` | Return from function, clean up Stack Frame. | Low |
| `DEFER` | Register cleanup resource (ensure energy return). | Medium |
| `SPAWN` | Launch logic in a separate Goroutine (Async). | High |

---

## ⚡ Energy Integration

In the Kitwork philosophy, **Every Opcode has a Cost**.
The VM loop increments the Energy Meter on every cycle:

```go
func (vm *VM) Run() {
    for {
        op := vm.fetch()
        vm.Energy -= EnergyTable[op] // Charge Energy
        if vm.Energy <= 0 {
            panic("Out of Energy")
        }
        vm.execute(op)
    }
}
```

This ensures that tight loops (`JUMP` backwards) or heavy allocations (`MAKE`) are strictly regulated by the available energy budget.

---
*© Kitwork Opcode - Core Virtual Machine*
