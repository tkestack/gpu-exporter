package nvml

import (
	"fmt"
	"os"
	"sync"
)

var (
	nvmlInitCounter int
	mux             sync.Mutex
)

func InitCounter() (cleanup func(), err error) {
	mux.Lock()
	if nvmlInitCounter < 0 {
		count := fmt.Sprintf("%d", nvmlInitCounter)
		err = fmt.Errorf("ShutdownCounter() is called %s times, before InitCounter()", count[1:])
	}
	if nvmlInitCounter == 0 {
		err = Init()
	}
	nvmlInitCounter += 1
	mux.Unlock()

	return func() {
		if err := ShutdownCounter(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to shutdown DCGM with error: `%v`", err)
		}
	}, err
}

func ShutdownCounter() (err error) {
	mux.Lock()
	if nvmlInitCounter <= 0 {
		err = fmt.Errorf("Init() needs to be called before Shutdown()")
	}
	if nvmlInitCounter == 1 {
		err = Shutdown()
	}
	nvmlInitCounter -= 1
	mux.Unlock()

	return
}
