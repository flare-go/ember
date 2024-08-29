package ember

import "context"

func (c *MultiCache) executeWithResilience(ctx context.Context, f func() error) error {

	_, err := c.globalCB.Execute(func() (any, error) {
		return nil, c.retrier.Run(ctx, f)
	})

	return err
}
