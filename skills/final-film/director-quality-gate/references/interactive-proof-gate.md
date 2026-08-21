# Interactive Proof Gate Reference

For every claimed interactive capability, require a bounded proof tuple:

`pre-state evidence → approved action evidence → post-state evidence`

At least two distinct interactions must produce independent region changes. Reversal or undo claims require approximate restoration of both the visual state and any associated displayed value. Responsive or touch claims require evidence from the declared viewport and input modality. Boundary-state claims require the state itself and the available next action to be visible.

Temporal visual observations are supporting Gate evidence only. They cannot replace browser action logs or approve an action. Terminal preview success requires one visual/frame channel plus one route, DOM, ARIA, or network channel.

Fail the automation attribution gate when browser input came from outside the authorized execution module. Keep the media for diagnosis, but do not label the run autonomous.
