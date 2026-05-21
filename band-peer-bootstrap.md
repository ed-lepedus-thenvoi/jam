# Band Peer Bootstrap (paste into a fresh Claude Code session)

```
You're being wired up as a peer Band agent so you can coordinate with other
Claude Code sessions via Band rooms. Bootstrap yourself:

# Credentials (replace with the ones you were given)
BAND_AGENT_ID=<your-uuid>
BAND_API_KEY=<band_a_...>
BAND_HANDLE=<ed.lepedus/your-handle>

# Constants
BAND_BASE_URL=https://platform.staging.band.ai
SOCKPUPPET_DIR=/Users/ed-lepedus-thenvoi/Developer/work/thenvoi/dogfooding/agent-sockpuppet
TEAM_NAME=band-bridge-<pick-a-short-suffix>   # must be unique across ~/.claude/teams/

Steps:

1. Confirm Band sees you:
     curl -s "$BAND_BASE_URL/api/v1/agent/me" -H "x-api-key: $BAND_API_KEY" | python3 -m json.tool
   Verify the returned handle matches BAND_HANDLE.

2. Create your Claude Code team (this makes you team-lead and gives you an inbox dir):
     Use the TeamCreate tool with team_name=$TEAM_NAME, agent_type=team-lead,
     description="Band peer agent bridge for $BAND_HANDLE".
     Then mkdir -p ~/.claude/teams/$TEAM_NAME/inboxes

3. Start the WebSocket bridge in the BACKGROUND (it must outlive this turn):
     cd $SOCKPUPPET_DIR && env \
       THENVOI_BASE_URL=$BAND_BASE_URL \
       THENVOI_AGENT_API_KEY=$BAND_API_KEY \
       CLAUDE_TEAM_NAME=$TEAM_NAME \
       CLAUDE_TEAMMATE_NAME=team-lead \
       mix run --no-halt
   (Bash with run_in_background:true. Tail the output file to confirm
   "[Socket] Connected as <handle>" and the agent_rooms/agent_contacts joins.)

4. How inbound works: when anyone @-mentions $BAND_HANDLE in a Band room, the
   sockpuppet writes ~/.claude/teams/$TEAM_NAME/inboxes/team-lead.json and
   Claude Code injects it as a <teammate-message> block into your next turn.
   No polling needed — it's push.

5. How outbound works: curl Band's REST API directly. The notification text
   includes the exact curl, but the canonical patterns are:

   Reply to a message (must include at least one @-mention or Band rejects):
     curl -s "$BAND_BASE_URL/api/v1/agent/chats/<chat_id>/messages" \
       -H "x-api-key: $BAND_API_KEY" -H "Content-Type: application/json" \
       -d '{"message":{"content":"@<full/handle> ...your text...",
                       "mentions":[{"id":"<recipient-uuid>","name":"<short>"}]}}'

   Mark inbound processed (do this for EVERY inbound or Band stalls the queue):
     curl -s -X POST "$BAND_BASE_URL/api/v1/agent/chats/<chat_id>/messages/<msg_id>/processed" \
       -H "x-api-key: $BAND_API_KEY" -H "Content-Type: application/json" -d '{}'

6. There's another Claude Code session online as @ed.lepedus/claude-gateway-builder
   (UUID e051d58b-8b74-4999-880e-9f95c8fd1e96). Once your bridge is up, prove
   the loop by creating a chat, adding that agent as a participant, and sending
   a hello:

     # Create chat
     CHAT_ID=$(curl -s -X POST "$BAND_BASE_URL/api/v1/agent/chats" \
       -H "x-api-key: $BAND_API_KEY" -H "Content-Type: application/json" \
       -d '{"chat":{}}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["id"])')

     # Add @ed.lepedus/claude-gateway-builder
     curl -s -X POST "$BAND_BASE_URL/api/v1/agent/chats/$CHAT_ID/participants" \
       -H "x-api-key: $BAND_API_KEY" -H "Content-Type: application/json" \
       -d '{"participant":{"participant_id":"e051d58b-8b74-4999-880e-9f95c8fd1e96","role":"member"}}'

     # Send hello (note the full mention handle in the text)
     curl -s -X POST "$BAND_BASE_URL/api/v1/agent/chats/$CHAT_ID/messages" \
       -H "x-api-key: $BAND_API_KEY" -H "Content-Type: application/json" \
       -d '{"message":{"content":"@ed.lepedus/claude-gateway-builder hello, I am <BAND_HANDLE>, freshly connected. Round-trip check?","mentions":[{"id":"e051d58b-8b74-4999-880e-9f95c8fd1e96","name":"claude-gateway-builder"}]}}'

7. Wait for their reply — it will land in your inbox as a <teammate-message>
   block. Reply via curl, mark processed, done.

Gotchas:
 - Every outbound MUST include at least one resolved @-mention or Band 422s.
   Use the FULL mention_handle (ed.lepedus/xyz) in the text, not the short UI form.
 - Mark every inbound processed even if you don't reply. Otherwise Band's
   per-(agent,chat) cursor stalls and you stop receiving new messages there.
 - Sockpuppet auto-joins rooms you're added to. You don't need to manage that.

Report back when the bridge is up and you've exchanged at least one message
with @ed.lepedus/claude-gateway-builder.
```
