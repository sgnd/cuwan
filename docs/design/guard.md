# Guard design

The guard runs first on every step of the live loop, before take-profit,
stop-loss and the on-exit mode. It has no flag, environment variable or bot
field. Its numbers are constants in `internal/live/guard.go`, and a test
fails if that file gains a package-level variable.

## Futures: preventive close

For the bot's own position (entry price `E`, leverage `L`, capped at 20x):

1. Liquidation price:
   - the venue's own liquidation price, when it gives one on the losing
     side of entry;
   - otherwise the estimate `E × (1 − 1/(2L))` for a long, `E × (1 + 1/(2L))`
     for a short. Venues keep the maintenance rate at or below half the
     initial rate (`1/L`) for the leverage they allow, so this estimate is
     at or before the real liquidation.
2. Gap: `max(0.05 × |E − liquidation|, 0.002 × E)`.
3. Trigger: liquidation + gap for a long, liquidation − gap for a short.
4. The guard compares the venue's mark price (the price it liquidates on)
   with the trigger, and flattens the bot when the mark reaches it.

| Case | Entry | Venue liquidation | Leverage | Side | Trigger |
|---|---|---|---|---|---|
| Venue price given | 100 | 96 | 20x | long | 96.2 |
| No venue price | 100 | none | 20x | long | 97.7 |
| Venue price given | 100 | 104 | 20x | short | 103.8 |
| No venue price | 100 | none | 10x | short | 104.75 |
| Minimum gap | 100 | 99.9 | 20x | long | 100.1 |
| Venue price on the wrong side | 100 | 101 | 20x | long | 97.7 |
| Leverage above the cap | 100 | none | 50x | long | 97.7 |
| Grid long, filled at 95 | 95 | none | 20x | long | 92.815 |

The table is `TestTriggerTable`. `TestTriggerIsBeforeRealLiquidation`
checks every leverage from 1x to 20x with maintenance rates up to half the
initial rate.

Before v0.0.1 the estimate was `E × (1 − 1/L)` with no maintenance margin,
on the last price. For a 20x long from 100 it fired at 95.25, after a real
liquidation near 97.5. The guard now closes earlier on purpose.

## Spot

A spot bot stops and sells back what it bought when the price falls more
than 5% below its range.

## Not covered

- The loop checks every 5 seconds. A move faster than that can pass the
  trigger before the next check.
- A whole older store file put back by someone who can write the folder
  opens at its older generation (see `internal/store`).
