package common

import "time"

type FinalAnswerWebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
}

type FinalAnswerWebhookPayload struct {
	Event       string     `json:"event"`
	Agent       string     `json:"agent"`
	ContextUID  ContextUID `json:"context_uid"`
	UserInput   string     `json:"user_input"`
	FinalAnswer string     `json:"final_answer"`
	Steps       []*Step    `json:"steps,omitempty"`
	GeneratedAt time.Time  `json:"generated_at"`
}
