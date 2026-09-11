package sync

import (
	"errors"
	"github.com/sviniabanditka/promin/server/internal/store"
)

// Watch queue (docs/miniapp.md): the phone lines items up, the TV pops the
// head when what it plays has no next episode. Every mutation publishes
// EventQueueUpdated with the whole list — the queue is small and a full
// snapshot keeps every client (TV, phones) trivially consistent.

func toQueueDTOs(rows []store.QueueItem) []QueueItemDTO {
	out := make([]QueueItemDTO, 0, len(rows))
	for _, it := range rows {
		out = append(out, toQueueDTO(it))
	}
	return out
}

func toQueueDTO(it store.QueueItem) QueueItemDTO {
	return QueueItemDTO{ID: it.ID, TMDBID: it.TMDBID, MediaType: it.MediaType, Season: it.Season, Episode: it.Episode, Position: it.Position}
}

func (s *Service) ListQueue(userID int64) ([]QueueItemDTO, error) {
	rows, err := s.queue.List(userID)
	if err != nil {
		return nil, err
	}
	return toQueueDTOs(rows), nil
}

func (s *Service) publishQueue(userID int64) error {
	list, err := s.ListQueue(userID)
	if err != nil {
		return err
	}
	s.hub.Publish(userID, EventQueueUpdated, map[string]any{"items": list})
	return nil
}

// AddQueue appends; a duplicate returns the existing item with created=false
// and publishes nothing.
// queueMax bounds a profile's watch queue: Move renumbers every row and every
// mutation republishes the whole list, so an unbounded queue is a CPU, DB
// and journal sink that one PIN can fill.
const queueMax = 200

var ErrQueueFull = errors.New("sync: queue full")

func (s *Service) AddQueue(userID, tmdbID int64, mediaType string, season, episode *int) (QueueItemDTO, bool, error) {
	if !validMediaType(mediaType) {
		return QueueItemDTO{}, false, ErrInvalidMediaType
	}
	if cur, err := s.queue.List(userID); err == nil && len(cur) >= queueMax {
		return QueueItemDTO{}, false, ErrQueueFull
	}
	if mediaType == "movie" {
		season, episode = nil, nil
	}
	it, created, err := s.queue.Add(userID, tmdbID, mediaType, season, episode, s.now().Unix())
	if err != nil {
		return QueueItemDTO{}, false, err
	}
	if created {
		if err := s.publishQueue(userID); err != nil {
			return QueueItemDTO{}, false, err
		}
	}
	return toQueueDTO(it), created, nil
}

func (s *Service) RemoveQueue(userID, id int64) error {
	if err := s.queue.Remove(userID, id); err != nil {
		return err
	}
	return s.publishQueue(userID)
}

func (s *Service) MoveQueue(userID, id int64, position int) error {
	if err := s.queue.Move(userID, id, position); err != nil {
		return err
	}
	return s.publishQueue(userID)
}

func (s *Service) ClearQueue(userID int64) error {
	if err := s.queue.Clear(userID); err != nil {
		return err
	}
	return s.publishQueue(userID)
}

// PopQueue removes and returns the head; ok=false when the queue is empty
// (nothing is published then).
func (s *Service) PopQueue(userID int64) (QueueItemDTO, bool, error) {
	it, ok, err := s.queue.Pop(userID)
	if err != nil || !ok {
		return QueueItemDTO{}, false, err
	}
	return toQueueDTO(it), true, s.publishQueue(userID)
}
