package main

import (
	"context"

	"github.com/BalanceBalls/nekot/util"
	tea "github.com/charmbracelet/bubbletea"
)

/** ChatbangClient implements nekot's LlmClient interface, routing prompts through CDP. */
type ChatbangClient struct {
	cdpCtx context.Context
}

func (c *ChatbangClient) RequestCompletion(
	ctx context.Context,
	chatMsgs []util.LocalStoreMessage,
	modelSettings util.Settings,
	resultChan chan util.ProcessApiCompletionResponse,
) tea.Cmd {
	return func() tea.Msg {
		go func() {
			if len(chatMsgs) == 0 {
				return
			}

			lastMsg := chatMsgs[len(chatMsgs)-1].Content
			id := util.GetNextProcessResultId(chatMsgs)

			response, err := sendAndWaitForResponse(c.cdpCtx, lastMsg)
			if err != nil {
				util.WriteToResponseChannel(ctx, resultChan, util.ProcessApiCompletionResponse{
					ID:  id,
					Err: err,
				})
				util.WriteToResponseChannel(ctx, resultChan, util.ProcessApiCompletionResponse{
					ID:    id + 1,
					Final: true,
				})
				return
			}

			// Chunk 1: content (Idle → ProcessingChunks)
			util.WriteToResponseChannel(ctx, resultChan, util.ProcessApiCompletionResponse{
				ID: id,
				Result: util.CompletionChunk{
					Model: "chatgpt",
					Choices: []util.Choice{{
						Delta: map[string]any{"content": response},
					}},
				},
			})

			// Chunk 2: stop signal (ProcessingChunks → AwaitingFinalization)
			util.WriteToResponseChannel(ctx, resultChan, util.ProcessApiCompletionResponse{
				ID: id + 1,
				Result: util.CompletionChunk{
					Model: "chatgpt",
					Choices: []util.Choice{{
						Delta:        map[string]any{},
						FinishReason: "stop",
					}},
				},
			})

			// Chunk 3: final signal (AwaitingFinalization → Finalized)
			util.WriteToResponseChannel(ctx, resultChan, util.ProcessApiCompletionResponse{
				ID:    id + 2,
				Final: true,
			})
		}()
		return nil
	}
}

func (c *ChatbangClient) RequestModelsList(ctx context.Context) util.ProcessModelsResponse {
	return util.ProcessModelsResponse{
		Result: util.ModelsListResponse{
			Data: []util.ModelDescription{{
				Id:     "chatgpt",
				Object: "model",
			}},
		},
	}
}
