package platform

import (
	"context"
	"log"
)

// PlatformManager registers platforms and drives them concurrently.
type PlatformManager struct {
	pipeline  *Pipeline
	platforms []Platform
}

// NewManager constructs a PlatformManager.
func NewManager(pipeline *Pipeline) *PlatformManager {
	return &PlatformManager{pipeline: pipeline}
}

// Register adds a platform to the manager.
func (m *PlatformManager) Register(p Platform) {
	m.platforms = append(m.platforms, p)
}

// StartAll launches each platform's receiver in its own goroutine and blocks until ctx is cancelled.
func (m *PlatformManager) StartAll(ctx context.Context) {
	for _, p := range m.platforms {
		p := p
		go func() {
			sender := p.Sender()
			dispatch := func(msg InboundMessage) {
				log.Printf("platform[%s]: dispatch received sessionKey=%s contentLen=%d", p.ID(), msg.SessionKey, len(msg.Content))
				if IsClearCommand(msg.Content) {
					// Handle synchronously without ack — result is instant.
					result := m.pipeline.Handle(context.Background(), msg)
					if err := sender.Send(context.Background(), OutboundMessage{
						SessionKey: msg.SessionKey,
						Content:    result,
						ReplyTo:    msg,
					}); err != nil {
						log.Printf("platform[%s]: send error: %v", p.ID(), err)
					}
					return
				}

				// Send ack immediately.
				if err := sender.Send(context.Background(), OutboundMessage{
					SessionKey: msg.SessionKey,
					Content:    AckMessage,
					ReplyTo:    msg,
				}); err != nil {
					log.Printf("platform[%s]: ack error: %v", p.ID(), err)
				} else {
					log.Printf("platform[%s]: ack sent sessionKey=%s", p.ID(), msg.SessionKey)
				}

				// Process and reply asynchronously.
				// Known limitation: uses context.Background() — goroutine outlives server shutdown.
				log.Printf("platform[%s]: async handler started sessionKey=%s", p.ID(), msg.SessionKey)
				go func() {
					result := m.pipeline.Handle(context.Background(), msg)
					if err := sender.Send(context.Background(), OutboundMessage{
						SessionKey: msg.SessionKey,
						Content:    result,
						ReplyTo:    msg,
					}); err != nil {
						log.Printf("platform[%s]: send error: %v", p.ID(), err)
					}
					log.Printf("platform[%s]: async handler done sessionKey=%s", p.ID(), msg.SessionKey)
				}()
			}

			log.Printf("platform[%s]: starting receiver", p.ID())
			if err := p.Receiver().Start(ctx, dispatch); err != nil && ctx.Err() == nil {
				log.Printf("platform[%s]: receiver error: %v", p.ID(), err)
			}
		}()
	}
	<-ctx.Done()
}
