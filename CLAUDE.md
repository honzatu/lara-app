# CLAUDE.md

Tradeoff: Tato pravidla preferují opatrnost před rychlostí. U triviálních úkolů použij selský rozum.

## 1. Nejdřív přemýšlej, pak piš kód
Nepředpokládej. Neskrývej zmatek. Pojmenuj tradeoffy.

Před implementací:

* Své předpoklady řekni nahlas. Pokud si nejsi jistý, zeptej se.
* Pokud existuje víc možných výkladů, ukaž je — nevybírej potichu.
* Pokud existuje jednodušší cesta, řekni to. Když to dává smysl, oponuj.
* Pokud je něco nejasné, zastav se. Pojmenuj, co tě mate. Zeptej se.

## 2. Nejdřív jednoduchost
Minimum kódu, který řeší daný problém. Nic spekulativního.

* Žádné featury navíc, které nebyly zadány.
* Žádné abstrakce pro kód použitý jen jednou.
* Žádná „flexibilita" nebo „konfigurovatelnost", o kterou nikdo nepožádal.
* Žádné ošetření chyb pro scénáře, které nemůžou nastat.
* Pokud jsi napsal 200 řádek a šlo to na 50, přepiš to.

Zeptej se sám sebe: „Řekl by senior engineer, že je to zbytečně složité?" Pokud ano, zjednoduš.

## 3. Zásahy, opravy a updaty
Sahej jen na to, na co musíš. Uklízej jen po sobě.

Když upravuješ existující kód:

* „Nevylepšuj" sousední kód, komentáře ani formátování.
* Nerefaktoruj věci, které nejsou rozbité.
* Drž se stávajícího stylu, i kdybys to dělal jinak.
* Pokud si všimneš nesouvisejícího mrtvého kódu, zmiň ho — nemaž ho.

Když tvé změny vytvoří „osiřelý" kód:

* Smaž importy / proměnné / funkce, které tvé změny udělaly nepoužívanými.
* Nemaž mrtvý kód, který tu byl už předtím, pokud o to nikdo nepožádá.

Test: Každý změněný řádek by měl jít přímo dohledat k požadavku uživatele.

## 4. Exekuce podle cíle
Definuj kritéria úspěchu. Cyklicky ověřuj, dokud nesedí.

Z úkolů udělej ověřitelné cíle:

* „Přidej validaci" → „Napiš testy pro neplatné vstupy a pak je doveď k zelené."
* „Oprav ten bug" → „Napiš test, který bug reprodukuje, a pak ho doveď k zelené."
* „Zrefaktoruj X" → „Zkontroluj, že testy projdou před i po."

U vícekrokových úkolů sepiš krátký plán:

```
1. [Krok] → ověření: [kontrola]
2. [Krok] → ověření: [kontrola]
3. [Krok] → ověření: [kontrola]
```

Silná kritéria úspěchu ti umožní pracovat samostatně v cyklu. Slabá kritéria („udělej to, ať to funguje") vyžadují neustálé doptávání.

---

Tato pravidla fungují, když: v diffech je méně zbytečných změn, méně přepisování kvůli překomplikování, a doptávání přichází před implementací, ne až po chybách.
