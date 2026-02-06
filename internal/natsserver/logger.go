package natsserver

import "github.com/rs/zerolog"

// zerologAdapter adapts zerolog to the nats server.Logger interface.
type zerologAdapter struct {
	logger zerolog.Logger
}

func newZerologAdapter(l zerolog.Logger) *zerologAdapter {
	return &zerologAdapter{logger: l.With().Str("component", "nats").Logger()}
}

func (z *zerologAdapter) Noticef(format string, v ...interface{}) {
	z.logger.Info().Msgf(format, v...)
}
func (z *zerologAdapter) Warnf(format string, v ...interface{}) {
	z.logger.Warn().Msgf(format, v...)
}
func (z *zerologAdapter) Fatalf(format string, v ...interface{}) {
	z.logger.Fatal().Msgf(format, v...)
}
func (z *zerologAdapter) Errorf(format string, v ...interface{}) {
	z.logger.Error().Msgf(format, v...)
}
func (z *zerologAdapter) Debugf(format string, v ...interface{}) {
	z.logger.Debug().Msgf(format, v...)
}
func (z *zerologAdapter) Tracef(format string, v ...interface{}) {
	z.logger.Trace().Msgf(format, v...)
}
