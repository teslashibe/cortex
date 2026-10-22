import json
from pathlib import Path
import tempfile
import unittest
from train import load_rows, private_path, input_text, checked_inputs


class SplitSafety(unittest.TestCase):
    def test_exact_budget_and_context_preservation(self):
        row = {'text': 'tea', 'context': 'I do not like tea at work'}
        self.assertEqual(input_text(row), 'Candidate: tea\nContext: I do not like [candidate] at work')
        tokenizer = lambda texts, **kwargs: {'input_ids': [list(x) for x in texts]}
        self.assertEqual(len(checked_inputs([row], tokenizer, 100)), 1)
        with self.assertRaises(ValueError):
            checked_inputs([row], tokenizer, 10)
        self.assertIn('tea at work', input_text(row, 'candidate-context-v1'))

    def fixture(self, directory):
        rows=[{'id':str(i),'group':str(i),'split':split,'text':'Fact '+str(i),
               'retention':'durable','kind':'fact','label_source':'synthetic'}
              for i,split in enumerate(['train','dev','test'])]
        path=Path(directory)/'data.jsonl'
        return rows,path

    def test_groups_and_duplicates_cannot_cross_splits(self):
        with tempfile.TemporaryDirectory() as d:
            for field in ['group','text']:
                rows,p=self.fixture(d)
                rows[1][field]=rows[0][field]
                p.write_text('\n'.join(json.dumps(r) for r in rows))
                with self.assertRaises(ValueError):load_rows(p)

    def test_teacher_is_provisional(self):
        with tempfile.TemporaryDirectory() as d:
            rows,p=self.fixture(d);rows[0]['label_source']='teacher'
            p.write_text('\n'.join(json.dumps(r) for r in rows))
            with self.assertRaises(ValueError):load_rows(p)
            self.assertEqual(len(load_rows(p,True)),3)

    def test_artifacts_refused_in_repository(self):
        with tempfile.TemporaryDirectory() as d:
            (Path(d)/'.git').mkdir()
            with self.assertRaises(ValueError):private_path(Path(d)/'weights')


if __name__=='__main__':unittest.main()
