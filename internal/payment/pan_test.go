package payment

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPANMasking(t *testing.T) {
	pan := PAN("2222405343248877")

	assert.Equal(t, "8877", pan.LastFour())

	masked := "**** **** **** 8877"
	assert.Equal(t, masked, fmt.Sprintf("%v", pan))
	assert.Equal(t, masked, fmt.Sprintf("%s", pan))
	assert.Equal(t, masked, fmt.Sprint(pan))

	b, err := json.Marshal(pan)
	assert.NoError(t, err)
	assert.Equal(t, `"`+masked+`"`, string(b))

	// Still masked when held by value inside another struct.
	wrapped := struct{ Card PAN }{Card: pan}
	assert.NotContains(t, fmt.Sprintf("%+v", wrapped), "2222405343248877")

	b, err = json.Marshal(wrapped)
	assert.NoError(t, err)
	assert.NotContains(t, string(b), "2222405343248877")
}

func TestPANShorterThanFourDigits(t *testing.T) {
	assert.Equal(t, "", PAN("123").LastFour())
	assert.Equal(t, "**** **** **** ", PAN("123").String())
}
