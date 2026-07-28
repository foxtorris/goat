package compression

import (
	"github.com/torrischen/goat/util/logging"

	"github.com/cloudwego/eino/schema"
	"github.com/pkoukk/tiktoken-go"
)

// ShouldCompress reports whether the messages exceed the configured model
// context threshold. modelMaxTokensK is measured in thousands of tokens.
func ShouldCompress(messages []*schema.AgenticMessage, modelMaxTokensK int) bool {
	tokenizer, err := tiktoken.GetEncoding(tiktoken.MODEL_O200K_BASE)
	if err != nil {
		logging.Warnf("Failed to get tokenizer: %v", err)
		totalChars := 0
		for _, message := range messages {
			totalChars += len(messagePlainText(message))
		}
		return totalChars/4 > modelMaxTokensK*1024
	}

	totalTokens := 0
	for _, message := range messages {
		totalTokens += len(tokenizer.EncodeOrdinary(messagePlainText(message)))
	}

	threshold := modelMaxTokensK * 1024
	shouldCompress := totalTokens > threshold
	if shouldCompress {
		logging.Infof("Token count %d exceeds threshold %d, compression needed", totalTokens, threshold)
	}
	return shouldCompress
}
