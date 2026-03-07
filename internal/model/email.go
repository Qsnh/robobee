package model

import "time"

type EmailDirection string

const (
	EmailInbound  EmailDirection = "inbound"
	EmailOutbound EmailDirection = "outbound"
)

type Email struct {
	ID          string         `json:"id" db:"id"`
	ExecutionID string         `json:"execution_id" db:"execution_id"`
	FromAddr    string         `json:"from_addr" db:"from_addr"`
	ToAddr      string         `json:"to_addr" db:"to_addr"`
	CCAddr      string         `json:"cc_addr,omitempty" db:"cc_addr"`
	Subject     string         `json:"subject" db:"subject"`
	Body        string         `json:"body" db:"body"`
	InReplyTo   string         `json:"in_reply_to,omitempty" db:"in_reply_to"`
	Direction   EmailDirection `json:"direction" db:"direction"`
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
}
