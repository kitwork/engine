package id

import (
	"fmt"
	"testing"
	"time"
)

func TestEntity(t *testing.T) {
	fmt.Println("--- Testing id.Entity() ---")
	for i := 0; i < 10; i++ {
		idVal := Entity()
		fmt.Printf("ID %d: %s (Length: %d)\n", i+1, idVal, len(idVal))
	}
	fmt.Println("---------------------------")
}

func TestDecodeEntity(t *testing.T) {
	fmt.Println("--- Testing id.DecodeEntity() ---")
	idVal := Entity()
	now := time.Now()
	
	decodedTime, err := DecodeEntity(idVal)
	if err != nil {
		t.Fatalf("Failed to decode entity: %v", err)
	}

	fmt.Printf("Generated ID: %s\n", idVal)
	fmt.Printf("Current Time: %v\n", now.UTC())
	fmt.Printf("Decoded Time: %v\n", decodedTime)
	
	// Difference should be extremely small (only missing the jitter/precision)
	diff := now.Sub(decodedTime)
	fmt.Printf("Time diff: %v\n", diff)
	
	if diff.Abs() > time.Millisecond*100 {
		t.Errorf("Time difference too large: %v", diff)
	}
	fmt.Println("---------------------------")
}
