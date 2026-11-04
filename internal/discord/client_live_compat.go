package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// discordgo rejects a whole message (and so a whole page of history) when it
// meets a message component type it does not know yet ("unknown component
// type: 20"). gocord never reads components, so on that error these helpers
// re-request the raw JSON and decode it with every "components" key removed.

func isUnknownComponent(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown component type")
}

// channelMessages is s.ChannelMessages with the unknown-component fallback.
func channelMessages(ctx context.Context, s *discordgo.Session, channelID string, limit int, before, after, around string) ([]*discordgo.Message, error) {
	ms, err := s.ChannelMessages(channelID, limit, before, after, around, discordgo.WithContext(ctx))
	if !isUnknownComponent(err) {
		return ms, err
	}
	uri := discordgo.EndpointChannelMessages(channelID)
	v := url.Values{}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	for k, val := range map[string]string{"before": before, "after": after, "around": around} {
		if val != "" {
			v.Set(k, val)
		}
	}
	if len(v) > 0 {
		uri += "?" + v.Encode()
	}
	body, err := s.RequestWithBucketID("GET", uri, nil, discordgo.EndpointChannelMessages(channelID), discordgo.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	var out []*discordgo.Message
	if err := decodeWithoutComponents(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// channelMessage is s.ChannelMessage with the unknown-component fallback.
func channelMessage(ctx context.Context, s *discordgo.Session, channelID, messageID string) (*discordgo.Message, error) {
	m, err := s.ChannelMessage(channelID, messageID, discordgo.WithContext(ctx))
	if !isUnknownComponent(err) {
		return m, err
	}
	body, err := s.RequestWithBucketID("GET", discordgo.EndpointChannelMessage(channelID, messageID), nil,
		discordgo.EndpointChannelMessage(channelID, ""), discordgo.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	var out discordgo.Message
	if err := decodeWithoutComponents(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// decodeWithoutComponents unmarshals body into out after deleting every
// "components" key at any depth (replies and forwarded snapshots carry them too).
func decodeWithoutComponents(body []byte, out any) error {
	var tree any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber() // keep integer fields (flags, permissions) exact
	if err := dec.Decode(&tree); err != nil {
		return fmt.Errorf("decode messages: %w", err)
	}
	stripComponents(tree)
	b, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf("decode messages: %w", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode messages: %w", err)
	}
	return nil
}

func stripComponents(v any) {
	switch t := v.(type) {
	case map[string]any:
		delete(t, "components")
		for _, child := range t {
			stripComponents(child)
		}
	case []any:
		for _, child := range t {
			stripComponents(child)
		}
	}
}
