package main

// topicStyles gives every experiment topic the style a user would pick for it.
// The Day 7 and Day 8 measurements ran all topics with one style, which put
// space topics under Earthside imagery. That was a mistake in the setup.
var topicStyles = map[string]string{
	"Rings of Saturn":         "Cosmos",
	"The Slow Moons":          "Cosmos",
	"Amber Lanterns":          "Myth",
	"The Quiet Library":       "Myth",
	"Velvet Meadow":           "Earthside",
	"Moss and Still Water":    "Earthside",
	"A Lighthouse at Dusk":    "Earthside",
	"Snow on Sleeping Fields": "Earthside",
}

// forcedStyle is set from the STYLE variable. When it is empty every topic
// gets its own style from topicStyles.
var forcedStyle string

func styleFor(topic string) string {
	if forcedStyle != "" {
		return forcedStyle
	}
	return topicStyles[topic]
}
