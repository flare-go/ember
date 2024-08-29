package ember

func (c *MultiCache) incrementAccessCount(key string) {

	if count, ok := c.accessCount.Load(key); ok {
		c.accessCount.Store(key, count.(int)+1)
	} else {
		c.accessCount.Store(key, 1)
	}
}
