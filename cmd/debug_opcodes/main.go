package main

import (
	"fmt"

	"github.com/kitwork/engine/opcode"
)

func main() {
	fmt.Printf("PUSH:    %d\n", opcode.PUSH)
	fmt.Printf("POP:     %d\n", opcode.POP)
	fmt.Printf("LOAD:    %d\n", opcode.LOAD)
	fmt.Printf("STORE:   %d\n", opcode.STORE)
	fmt.Printf("GET:     %d\n", opcode.GET)
	fmt.Printf("ADD:     %d\n", opcode.ADD)
	fmt.Printf("SUB:     %d\n", opcode.SUB)
	fmt.Printf("MUL:     %d\n", opcode.MUL)
	fmt.Printf("DIV:     %d\n", opcode.DIV)
	fmt.Printf("COMPARE: %d\n", opcode.COMPARE)
	fmt.Printf("JUMP:    %d\n", opcode.JUMP)
	fmt.Printf("UNLESS:  %d\n", opcode.UNLESS)
	fmt.Printf("HALT:    %d\n", opcode.HALT)
	fmt.Printf("MAKE:    %d\n", opcode.MAKE)
	fmt.Printf("SET:     %d\n", opcode.SET)
	fmt.Printf("CALL:    %d\n", opcode.CALL)
	fmt.Printf("INVOKE:  %d\n", opcode.INVOKE)
	fmt.Printf("LAMBDA:  %d\n", opcode.LAMBDA)
	fmt.Printf("RETURN:  %d\n", opcode.RETURN)
}
