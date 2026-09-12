import { describe, expect, it } from 'vitest'
import { resolveDocLink } from './docLinks'

// Regression: an https link inside a rendered docs page navigated the
// app's own webview to the external site. Every anchor class must be
// resolved deliberately.
describe('resolveDocLink', () => {
  it('classifies absolute URLs as external', () => {
    expect(resolveDocLink('start-here/install.md', 'https://github.com/millhq/mill/releases'))
      .toEqual({ kind: 'external', url: 'https://github.com/millhq/mill/releases' })
  })

  it('resolves a parent-relative .md cross-link against the current page directory', () => {
    expect(resolveDocLink('start-here/first-workflow.md', '../concepts/guardrails.md'))
      .toEqual({ kind: 'page', rel: 'concepts/guardrails.md' })
  })

  it('resolves a sibling .md cross-link', () => {
    expect(resolveDocLink('start-here/what-is-mill.md', 'install.md'))
      .toEqual({ kind: 'page', rel: 'start-here/install.md' })
    expect(resolveDocLink('concepts/workflows-and-steps.md', 'configure.md'))
      .toEqual({ kind: 'page', rel: 'concepts/configure.md' })
  })

  it('ignores anchors that are neither external nor .md pages', () => {
    expect(resolveDocLink('start-here/install.md', '#heading')).toEqual({ kind: 'ignore' })
    expect(resolveDocLink('start-here/install.md', '../../main.go')).toEqual({ kind: 'ignore' })
  })

  it('refuses traversal above the docs root', () => {
    expect(resolveDocLink('start-here/install.md', '../../../etc/passwd.md')).toEqual({ kind: 'ignore' })
  })
})
