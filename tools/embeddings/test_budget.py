import unittest
from server import segments, checked_texts


class BudgetTests(unittest.TestCase):
    def test_complete_original_coverage(self):
        for text in ['界🙂' * 900, 'a b c ' * 900, 'x', 'x' * 513]:
            count = lambda s: len(s) * 3 + 2
            spans = segments(text, count, 256)
            covered = set()
            for w in spans:
                self.assertEqual(w['tokens'], count(text[w['start_rune']:w['end_rune']]))
                self.assertLessEqual(w['tokens'], 256)
                covered.update(range(w['start_rune'], w['end_rune']))
            self.assertEqual(covered, set(range(len(text))))

    def test_no_truncation_fallback(self):
        with self.assertRaises(ValueError):
            checked_texts(['x' * 300], len, 256)
        with self.assertRaises(ValueError):
            segments('x', lambda _: 300, 256)
        self.assertEqual(checked_texts(['abc'], len, 256), ['abc'])

    def test_dense_region_does_not_stall_overlap(self):
        text = 'a' * 510 + '界' * 500
        count = lambda s: 2 + sum(1 if c == 'a' else 10 for c in s)
        spans = segments(text, count, 520)
        end = 0
        for span in spans:
            self.assertLessEqual(span['start_rune'], end)
            self.assertGreater(span['end_rune'], end)
            end = span['end_rune']
        self.assertEqual(end, len(text))


if __name__ == '__main__':
    unittest.main()
