"""Generate fabricated classification controls, never private session data."""
from pathlib import Path
import argparse, json, hashlib, os
from train import private_path
os.umask(0o077)
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output',required=True)
args=parser.parse_args()
root=private_path(args.output)
root.mkdir(parents=True,exist_ok=False)
values={
'fact':['My degree subject is geology','My home city is Tallinn','My employer is Cedar Works','My first language is Spanish','My bicycle is a Brompton','My cat is named Juniper','My laptop runs Fedora','My birthplace is Perth','My professional certification is in accounting','My keyboard layout is Colemak','My graduation year is 2016','My phone is a Fairphone'],
'preference':['I prefer short answers','I like oolong more than coffee','I want invoices in XML','I prefer metric measurements','I would rather use dark mode','I prefer quarterly billing','I like written updates instead of calls','I favor unsweetened drinks','I prefer code examples in Go','I like aisle seats','I prefer quiet restaurants','I want dates written year first'],
'project':['I am building a wildlife tracking application','I am leading the Cedar database migration','I am preparing the museum exhibition','I am developing a community weather station','I am writing a guide to ceramics','I am coordinating the station renovation','I am launching the Orchard newsletter','I am maintaining the Beacon package','I am organizing the coastal survey','I am designing the library catalog','I am assembling the geology curriculum','I am running the accessibility redesign'],
'relationship':['Mara is my sister','Ivo is my brother','Ari is my manager','Priya is my mentor','Leo is my cousin','Sofia is my business partner','Nico is my accountant','Asha is my thesis supervisor','Lena is my spouse','Noah is my father','Ruth is my colleague','Emil is my housemate'],
'routine':['I swim every Thursday','I volunteer every Sunday morning','I take pottery classes on Tuesdays','I run at dawn each weekday','I review invoices every Friday','I call my parents each Saturday','I attend language class every Monday','I water my garden each evening','I cycle to work on Wednesdays','I meditate each morning','I go to the library every fortnight','I bake bread each weekend'],
'behavior':['I tend to ask for evidence before deciding','I usually check assumptions before estimating','I often revise my notes after meetings','I tend to compare alternatives before buying','I habitually test a small sample first','I often ask colleagues to challenge my reasoning','I tend to avoid changing several variables together','I habitually document why I changed my mind','I usually sketch an outline before writing','I tend to reread instructions before starting','I often prototype an idea before committing','I usually double check unfamiliar terminology']}
wrappers={
'durable':['{x}.','A detail worth remembering: {x}.','For future reference, {x}.','Some stable background about me: {x}.','Here is something relevant about me: {x}.','Keep this as part of my background: {x}.'],
 'temporary':['For this task only, summarize the notes about {x}.','In this response only, include one sentence about {x}.','For today\'s exercise, give me an outline on {x}.','For this one draft, create a heading about {x}.','For the current worksheet only, list three points on {x}.','For this single request, produce a short description of {x}.'],
 'ignore':['This is a fictional example, not about me: "{x}."','A character in this story says "{x}." None of this is my biography.','Do not attribute this quotation to me: "{x}."','For a role-playing exercise, the imaginary narrator says "{x}."','This is an invented dialogue: "{x}." It does not describe me.','Here is sample text from a made-up person: "{x}."'],
 'uncertain':['I cannot confirm whether {x}. Please do not assume it.','I am not sure if {x}; this needs confirmation.','It is possible that {x}, but I have not verified that.','I need to check whether {x}; treat it as unresolved.','Someone suggested that {x}, but I do not know if it is true.','There is insufficient information to say whether {x}.']}
rows=[]
for retention,templates in wrappers.items():
 for t,template in enumerate(templates):
  split='train' if t<4 else 'dev' if t==4 else 'test'
  for ki,(kind,facts) in enumerate(values.items()):
   # Other retention labels are balanced with the full durable set.
   chosen=facts if retention=='durable' else facts[:2]
   for v,fact in enumerate(chosen):
    topic=['coastal geology','bicycle maintenance','museum archives','cloud formation','ceramic glazes','garden irrigation','language learning','wildlife surveys','library catalogs','weather instruments','footpath design','music theory'][(ki*2+v)%12]
    text=template.format(x=topic if retention=='temporary' else fact[0].lower()+fact[1:])
    label=kind if retention=='durable' else 'project' if retention=='temporary' else None
    rows.append({'id':hashlib.sha256(text.encode()).hexdigest()[:20],'group':f'synthetic-{retention}-{t}', 'split':split,'text':text,'context':text,'retention':retention,'kind':label,'label_source':'synthetic'})
# Remove repeated temporary topics produced by the kind loop.
rows=list({r['id']:r for r in rows}.values())
(root/'classification-synthetic.jsonl').write_text('\n'.join(json.dumps(r) for r in rows)+'\n')
(root/'synthetic-protocol.json').write_text(json.dumps({'description':'Fabricated templates with intended labels, held out by wrapper family; neither human annotations nor a natural-language benchmark. Temporary candidates are task requests, all labeled project.','rows':len(rows),'splits':{s:sum(r['split']==s for r in rows) for s in ['train','dev','test']},'generator_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest()},indent=2))
print('synthetic rows',len(rows))
