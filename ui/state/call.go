package state

// CallTitle is the call bar's title: the voice channel, plus "@ server" when
// the call is on a server other than the viewed one (elsewhere). With no
// server label it is just the channel.
func CallTitle(channel, server string, elsewhere bool) string {
	if !elsewhere || server == "" {
		return channel
	}
	return channel + " @ " + server
}
